package waveform_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/waveform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresCommitterPublishesWaveformJobObjectAndAuditAtomically(t *testing.T) {
	pool := migratedWaveformPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID = "10000000-0000-4000-8000-000000000071"
		otherSpace  = "10000000-0000-4000-8000-000000000072"
		userID      = "20000000-0000-4000-8000-000000000071"
		assetID     = "30000000-0000-4000-8000-000000000071"
		originalID  = "40000000-0000-4000-8000-000000000071"
		jobID       = "50000000-0000-4000-8000-000000000071"
		auditID     = "60000000-0000-4000-8000-000000000071"
	)
	seed := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')`, []any{workspaceID, otherSpace}},
		{`INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'waveform@example.test', 'hash', 'active')`, []any{userID}},
		{`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{workspaceID, userID}},
		{`INSERT INTO assets (id, workspace_id, title, language, status, duration_ms, created_by) VALUES ($1, $2, 'Waveform source', 'en', 'ready', 1000, $3)`, []any{assetID, workspaceID, userID}},
		{`INSERT INTO asset_objects (id, asset_id, kind, storage_backend, storage_key, mime_type, container, codec, duration_ms, file_size, sha256, creation_source, encryption_state) VALUES ($1, $2, 'original', 'local', 'objects/source.wav', 'audio/wav', 'wav', 'pcm_s16le', 1000, 64, repeat('a', 64), 'upload', 'none')`, []any{originalID, assetID}},
		{`INSERT INTO jobs (id, workspace_id, asset_id, created_by, kind, state, payload, max_attempts) VALUES ($1, $2, $3, $4, 'generate_waveform', 'queued', jsonb_build_object('asset_id', $3::uuid::text), 3)`, []any{jobID, workspaceID, assetID, userID}},
	}
	for _, statement := range seed {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed waveform commit: %v", err)
		}
	}
	now := time.Now().UTC()
	jobRepository := job.NewPostgresRepository(pool)
	claimed, err := jobRepository.Claim(ctx, job.ClaimParams{
		Kind: job.KindGenerateWaveform, WorkerID: "waveform-worker-1",
		Now: now, LeaseDuration: 2 * time.Minute,
	})
	if err != nil || claimed.ID != jobID {
		t.Fatalf("Claim() = (%+v, %v)", claimed, err)
	}
	committer := waveform.NewPostgresCommitter(pool)
	if err := committer.Commit(ctx, waveform.CommitParams{
		JobID: jobID, WorkerID: "waveform-worker-1", WorkspaceID: workspaceID,
		AssetID: assetID, ActorID: userID, AuditID: auditID,
		Object: storage.Object{
			Backend: storage.BackendS3,
			Key:     "immutable/waveform.png", Size: 128, SHA256: strings.Repeat("b", 64),
		},
		Width: waveform.Width, Height: waveform.Height, Now: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	var parentID, backend, mimeType, state, outcome, action, targetID string
	var durationMS int64
	if err := pool.QueryRow(ctx, `
		SELECT object.parent_object_id::text, object.storage_backend, object.mime_type, object.duration_ms,
		       queued.state, attempt.outcome, audit.action, audit.target_id::text
		FROM asset_objects object
		JOIN jobs queued ON queued.id = object.id
		JOIN job_attempts attempt ON attempt.job_id = queued.id AND attempt.attempt = 1
		JOIN audit_logs audit ON audit.id = $2
		WHERE object.id = $1 AND object.kind = 'waveform'`, jobID, auditID,
	).Scan(&parentID, &backend, &mimeType, &durationMS, &state, &outcome, &action, &targetID); err != nil {
		t.Fatalf("query committed waveform: %v", err)
	}
	if parentID != originalID || backend != string(storage.BackendS3) || mimeType != "image/png" || durationMS != 1000 ||
		state != job.StateSucceeded || outcome != "succeeded" || action != "waveform.generated" || targetID != jobID {
		t.Fatalf("committed waveform = %q/%q/%q/%d/%q/%q/%q/%q", parentID, backend, mimeType, durationMS, state, outcome, action, targetID)
	}
	repository := waveform.NewPostgresRepository(pool)
	if stored, err := repository.Get(ctx, workspaceID, assetID); err != nil || stored.ObjectID != jobID || stored.StorageBackend != storage.BackendS3 {
		t.Fatalf("Get() = (%+v, %v)", stored, err)
	}
	if _, err := repository.Get(ctx, otherSpace, assetID); !errors.Is(err, waveform.ErrNotFound) {
		t.Fatalf("cross-workspace Get() error = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE assets SET deleted_at = clock_timestamp(), status = 'trashed' WHERE id = $1`, assetID); err != nil {
		t.Fatalf("trash waveform asset: %v", err)
	}
	if _, err := repository.Get(ctx, workspaceID, assetID); !errors.Is(err, waveform.ErrNotFound) {
		t.Fatalf("trashed Get() error = %v", err)
	}
}

func migratedWaveformPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal("connect to PostgreSQL")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := fmt.Sprintf("waveform_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal("create isolated schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema")
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("create pool")
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire migration connection")
	}
	files, err := migration.Load(filepath.Join("..", "..", "migrations"))
	if err != nil {
		connection.Release()
		t.Fatalf("load migrations: %v", err)
	}
	if _, err := migration.Apply(ctx, connection.Conn(), files); err != nil {
		connection.Release()
		t.Fatalf("apply migrations: %v", err)
	}
	connection.Release()
	return pool
}
