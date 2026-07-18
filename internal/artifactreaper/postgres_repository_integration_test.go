package artifactreaper_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/artifactreaper"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	reaperWorkspaceID = "10000000-0000-4000-8000-000000000081"
	reaperUserID      = "20000000-0000-4000-8000-000000000081"
	reaperAssetID     = "30000000-0000-4000-8000-000000000081"
	reaperClipID      = "40000000-0000-4000-8000-000000000081"
	reaperExportID    = "50000000-0000-4000-8000-000000000081"
	reaperFutureID    = "60000000-0000-4000-8000-000000000081"
	reaperTranscript  = "70000000-0000-4000-8000-000000000081"
	reaperRevision    = "80000000-0000-4000-8000-000000000081"
	reaperLifecycleID = "a0000000-0000-4000-8000-000000000081"
)

func TestPostgresRepositoryConditionallyDeletesExpiredArtifactsAndAudits(t *testing.T) {
	pool := migratedReaperPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	seedReaperFixtures(t, ctx, pool, now)
	repository := artifactreaper.NewPostgresRepository(pool)

	artifacts, err := repository.ListExpired(ctx, now, 25)
	if err != nil {
		t.Fatalf("ListExpired() error = %v", err)
	}
	if len(artifacts) != 2 || artifacts[0].ID != reaperClipID || artifacts[1].ID != reaperExportID {
		t.Fatalf("ListExpired() = %+v", artifacts)
	}

	wrong := artifacts[0]
	wrong.SHA256 = strings.Repeat("f", 64)
	if deleted, err := repository.DeleteExpired(
		ctx, wrong, "90000000-0000-4000-8000-000000000081", now,
	); err != nil || deleted {
		t.Fatalf("mismatched DeleteExpired() = (%t, %v)", deleted, err)
	}
	if deleted, err := repository.DeleteExpired(
		ctx, artifacts[0], "90000000-0000-4000-8000-000000000082", now,
	); err != nil || !deleted {
		t.Fatalf("clip DeleteExpired() = (%t, %v)", deleted, err)
	}
	if deleted, err := repository.DeleteExpired(
		ctx, artifacts[0], "90000000-0000-4000-8000-000000000083", now,
	); err != nil || deleted {
		t.Fatalf("replayed DeleteExpired() = (%t, %v)", deleted, err)
	}
	if deleted, err := repository.DeleteExpired(
		ctx, artifacts[1], "90000000-0000-4000-8000-000000000084", now,
	); err != nil || !deleted {
		t.Fatalf("export DeleteExpired() = (%t, %v)", deleted, err)
	}

	var expiredObjects, futureObjects, auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM asset_objects WHERE id IN ($1, $2)`,
		reaperClipID, reaperExportID,
	).Scan(&expiredObjects); err != nil {
		t.Fatalf("count expired objects: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM asset_objects WHERE id = $1`, reaperFutureID).
		Scan(&futureObjects); err != nil {
		t.Fatalf("count future objects: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE actor_type = 'system' AND action = 'artifact.reaped'
		  AND target_id IN ($1, $2)
		  AND metadata->>'artifact_kind' IN ('clip', 'export')`,
		reaperClipID, reaperExportID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count reaper audits: %v", err)
	}
	if expiredObjects != 0 || futureObjects != 1 || auditCount != 2 {
		t.Fatalf(
			"post-reaper counts expired=%d future=%d audits=%d",
			expiredObjects, futureObjects, auditCount,
		)
	}
}

func TestReaperRemovesExpiredLocalArtifactEndToEnd(t *testing.T) {
	pool := migratedReaperPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	now := time.Now().UTC()
	seedReaperFixtures(t, ctx, pool, now)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("create local store: %v", err)
	}
	object, err := store.PutImmutable(
		ctx, reaperAssetID, reaperLifecycleID, storage.ObjectKindExport,
		bytes.NewReader([]byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nReap me\n")), 1024,
	)
	if err != nil {
		t.Fatalf("store lifecycle artifact: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			file_size, sha256, creation_source, encryption_state
		) VALUES ($1, $2, 'export', 'local', $3, 'text/vtt', $4, $5,
		          'transcript_export', 'none')`,
		reaperLifecycleID, reaperAssetID, object.Key, object.Size, object.SHA256,
	); err != nil {
		t.Fatalf("insert lifecycle object: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO transcript_exports (
			id, workspace_id, asset_id, revision_id, format, created_by,
			created_at, expires_at
		) VALUES ($1, $2, $3, $4, 'vtt', $5, $6, $7)`,
		reaperLifecycleID, reaperWorkspaceID, reaperAssetID, reaperRevision,
		reaperUserID, now.Add(-2*time.Hour), now.Add(-time.Hour),
	); err != nil {
		t.Fatalf("insert lifecycle export: %v", err)
	}

	reaper := artifactreaper.New(artifactreaper.NewPostgresRepository(pool), store)
	if processed, err := reaper.RunOnce(ctx); err != nil || !processed {
		t.Fatalf("RunOnce() = (%t, %v), want processed success", processed, err)
	}
	if file, err := store.Open(ctx, object.Key); err == nil {
		_ = file.Close()
		t.Fatal("expired lifecycle file remains")
	}
	var objectCount, sourceRevisionCount, auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM asset_objects WHERE id = $1`, reaperLifecycleID).
		Scan(&objectCount); err != nil {
		t.Fatalf("count lifecycle object: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM transcript_revisions WHERE id = $1`, reaperRevision).
		Scan(&sourceRevisionCount); err != nil {
		t.Fatalf("count source revision: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE action = 'artifact.reaped' AND target_id = $1`, reaperLifecycleID,
	).Scan(&auditCount); err != nil {
		t.Fatalf("count lifecycle audit: %v", err)
	}
	if objectCount != 0 || sourceRevisionCount != 1 || auditCount != 1 {
		t.Fatalf(
			"lifecycle counts object=%d source_revision=%d audit=%d",
			objectCount, sourceRevisionCount, auditCount,
		)
	}
}

func seedReaperFixtures(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	now time.Time,
) {
	t.Helper()
	statements := []struct {
		name string
		sql  string
		args []any
	}{
		{
			"workspace",
			`INSERT INTO workspaces (id, name) VALUES ($1, 'Reaper workspace')`,
			[]any{reaperWorkspaceID},
		},
		{
			"user",
			`INSERT INTO users (id, email, password_hash, status)
			 VALUES ($1, 'reaper-owner@example.com', 'encoded', 'active')`,
			[]any{reaperUserID},
		},
		{
			"membership",
			`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
			[]any{reaperWorkspaceID, reaperUserID},
		},
		{
			"asset",
			`INSERT INTO assets (
				id, workspace_id, title, language, status, duration_ms, created_by
			 ) VALUES ($1, $2, 'Reaper source', 'en-US', 'ready', 10000, $3)`,
			[]any{reaperAssetID, reaperWorkspaceID, reaperUserID},
		},
		{
			"transcript",
			`INSERT INTO transcripts (id, asset_id, language) VALUES ($1, $2, 'en-US')`,
			[]any{reaperTranscript, reaperAssetID},
		},
		{
			"revision",
			`INSERT INTO transcript_revisions (
				id, transcript_id, kind, text_content, created_by
			 ) VALUES ($1, $2, 'raw_asr', 'Permanent source transcript', $3)`,
			[]any{reaperRevision, reaperTranscript, reaperUserID},
		},
		{
			"derived objects",
			`INSERT INTO asset_objects (
				id, asset_id, kind, storage_backend, storage_key, mime_type,
				file_size, sha256, creation_source, encryption_state
			 ) VALUES
				($1, $4, 'clip', 'local', 'objects/reaper/clip', 'audio/wav', 10, repeat('a', 64), 'agent_clip', 'none'),
				($2, $4, 'export', 'local', 'objects/reaper/export', 'text/vtt', 11, repeat('b', 64), 'transcript_export', 'none'),
				($3, $4, 'clip', 'local', 'objects/reaper/future', 'audio/wav', 12, repeat('c', 64), 'agent_clip', 'none')`,
			[]any{reaperClipID, reaperExportID, reaperFutureID, reaperAssetID},
		},
		{
			"clip lifetimes",
			`INSERT INTO audio_clips (
				id, workspace_id, asset_id, start_ms, end_ms, created_by, created_at, expires_at
			 ) VALUES
				($1, $3, $4, 0, 1000, $5, $6, $7),
				($2, $3, $4, 1000, 2000, $5, $6, $8)`,
			[]any{
				reaperClipID, reaperFutureID, reaperWorkspaceID, reaperAssetID,
				reaperUserID, now.Add(-4 * time.Hour), now.Add(-2 * time.Hour),
				now.Add(time.Hour),
			},
		},
		{
			"export lifetime",
			`INSERT INTO transcript_exports (
				id, workspace_id, asset_id, revision_id, format, created_by, created_at, expires_at
			 ) VALUES ($1, $2, $3, $4, 'vtt', $5, $6, $7)`,
			[]any{
				reaperExportID, reaperWorkspaceID, reaperAssetID, reaperRevision,
				reaperUserID, now.Add(-4 * time.Hour), now.Add(-time.Hour),
			},
		},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed %s: %v", statement.name, err)
		}
	}
}

func migratedReaperPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
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
	schema := fmt.Sprintf("artifact_reaper_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema: %v", err)
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
