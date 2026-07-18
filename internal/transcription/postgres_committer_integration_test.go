package transcription_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
	"github.com/getio0909/voice-asset-server/internal/transcription"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresCommitterAtomicallyPublishesRawTranscriptAndSucceedsJob(t *testing.T) {
	pool := migratedTranscriptionPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	var now time.Time
	if err := pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal("read database clock")
	}
	const (
		workspaceID          = "10000000-0000-4000-8000-000000000071"
		userID               = "20000000-0000-4000-8000-000000000071"
		assetID              = "30000000-0000-4000-8000-000000000071"
		originalID           = "40000000-0000-4000-8000-000000000071"
		jobID                = "50000000-0000-4000-8000-000000000071"
		segmentID            = "60000000-0000-4000-8000-000000000071"
		normalizedRevisionID = "50000000-0000-4000-8000-000000000072"
		normalizedSegmentID  = "60000000-0000-4000-8000-000000000072"
		auditID              = "70000000-0000-4000-8000-000000000071"
		workerID             = "worker-1"
	)
	seed := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO workspaces (id, name) VALUES ($1, 'Primary')", []any{workspaceID}},
		{"INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'owner@example.com', 'encoded', 'active')", []any{userID}},
		{"INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')", []any{workspaceID, userID}},
		{"INSERT INTO assets (id, workspace_id, title, language, status, duration_ms, created_by) VALUES ($1, $2, 'Recording', 'en-US', 'processing', 1000, $3)", []any{assetID, workspaceID, userID}},
		{`INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			file_size, sha256, creation_source, encryption_state
		) VALUES ($1, $2, 'original', 'local', 'objects/original.wav', 'audio/wav',
			12, repeat('b', 64), 'upload', 'none')`, []any{originalID, assetID}},
		{`INSERT INTO jobs (
			id, workspace_id, asset_id, created_by, kind, state, attempts,
			max_attempts, lease_owner, lease_expires_at
		) VALUES ($1, $2, $3, $4, 'mock_transcribe', 'running', 1, 3, $5, $6)`, []any{jobID, workspaceID, assetID, userID, workerID, now.Add(5 * time.Minute)}},
		{"INSERT INTO job_attempts (job_id, attempt, worker_id, started_at) VALUES ($1, 1, $2, $3)", []any{jobID, workerID, now.Add(-time.Second)}},
	}
	for _, statement := range seed {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed transcription fixture")
		}
	}
	confidence := 0.99
	speaker := "speaker-1"
	params := transcription.CommitRawParams{
		JobID: jobID, WorkerID: workerID, WorkspaceID: workspaceID,
		AssetID: assetID, ActorID: userID,
		TranscriptID: assetID, RevisionID: jobID, NormalizedRevisionID: normalizedRevisionID,
		RawObjectID: jobID, AuditID: auditID,
		Language: "en-US", Text: "Welcome to VoiceAsset.",
		ProviderID:       "tencent_asr",
		ProviderSnapshot: json.RawMessage(`{"provider_id":"mock_asr","version":"1"}`),
		HotwordSnapshot:  json.RawMessage(`{"sets":[{"id":"set-1","version":2}]}`),
		RawObject: storage.Object{
			Backend: storage.BackendS3, Key: "objects/provider/raw.json", Size: 128,
			SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Segments: []transcript.Segment{{
			ID: segmentID, Ordinal: 0, StartMS: 0, EndMS: 1000,
			Speaker: &speaker, Text: "Welcome to VoiceAsset.", Confidence: &confidence,
			Words: json.RawMessage(`[{"text":"Welcome","start_ms":0,"end_ms":400,"confidence":0.99}]`),
		}},
		NormalizedSegments: []transcript.Segment{{
			ID: normalizedSegmentID, Ordinal: 0, StartMS: 0, EndMS: 1000,
			Speaker: &speaker, Text: "Welcome to VoiceAsset.", Confidence: &confidence,
			Words: json.RawMessage(`[{"text":"Welcome","start_ms":0,"end_ms":400,"confidence":0.99}]`),
		}},
		Now: now,
	}
	committer := transcription.NewPostgresCommitter(pool)
	revision, err := committer.CommitRaw(ctx, params)
	if err != nil {
		t.Fatalf("CommitRaw() error = %v", err)
	}
	if revision.ID != normalizedRevisionID || revision.ParentRevisionID != jobID ||
		revision.Kind != transcript.KindNormalized || revision.TranscriptID != assetID ||
		!equalJSON(revision.HotwordSnapshot, params.HotwordSnapshot) || len(revision.Segments) != 1 {
		t.Fatalf("CommitRaw() = %+v", revision)
	}
	var (
		jobState       string
		resultRevision string
		leaseOwner     *string
		attemptOutcome string
		assetState     string
	)
	if err := pool.QueryRow(ctx, "SELECT state, result_revision_id::text, lease_owner FROM jobs WHERE id = $1", jobID).Scan(&jobState, &resultRevision, &leaseOwner); err != nil {
		t.Fatalf("query completed job")
	}
	if err := pool.QueryRow(ctx, "SELECT outcome FROM job_attempts WHERE job_id = $1 AND attempt = 1", jobID).Scan(&attemptOutcome); err != nil {
		t.Fatalf("query completed attempt")
	}
	if err := pool.QueryRow(ctx, "SELECT status FROM assets WHERE id = $1", assetID).Scan(&assetState); err != nil {
		t.Fatalf("query completed asset")
	}
	if jobState != job.StateSucceeded || resultRevision != normalizedRevisionID || leaseOwner != nil ||
		attemptOutcome != "succeeded" || assetState != "ready" {
		t.Fatalf("completion state = job:%s revision:%s lease:%v attempt:%s asset:%s",
			jobState, resultRevision, leaseOwner, attemptOutcome, assetState)
	}
	stored, err := transcript.NewPostgresRepository(pool).GetRevision(ctx, workspaceID, normalizedRevisionID)
	if err != nil || stored.ParentRevisionID != jobID || stored.ProviderRawObjectID != "" ||
		!equalJSON(stored.HotwordSnapshot, params.HotwordSnapshot) || len(stored.Segments) != 1 {
		t.Fatalf("GetRevision() = (%+v, %v)", stored, err)
	}
	rawStored, err := transcript.NewPostgresRepository(pool).GetRevision(ctx, workspaceID, jobID)
	if err != nil || rawStored.ProviderRawObjectID != jobID || rawStored.Kind != transcript.KindRawASR {
		t.Fatalf("GetRevision(raw) = (%+v, %v)", rawStored, err)
	}
	var creationSource, storageBackend string
	if err := pool.QueryRow(ctx, "SELECT creation_source, storage_backend FROM asset_objects WHERE id = $1", jobID).Scan(&creationSource, &storageBackend); err != nil {
		t.Fatal("query provider raw object source")
	}
	if creationSource != params.ProviderID || storageBackend != string(storage.BackendS3) {
		t.Fatalf("provider raw object metadata = %q/%q", creationSource, storageBackend)
	}
	if _, err := pool.Exec(ctx, "UPDATE asset_objects SET storage_key = 'tampered' WHERE id = $1", jobID); err == nil {
		t.Fatal("provider raw response accepted UPDATE")
	}
	if _, err := pool.Exec(ctx, "UPDATE transcript_revisions SET text_content = 'tampered' WHERE id = $1", jobID); err == nil {
		t.Fatal("raw transcript revision accepted UPDATE")
	}
	if _, err := pool.Exec(ctx, "UPDATE transcript_revisions SET text_content = 'tampered' WHERE id = $1", normalizedRevisionID); err == nil {
		t.Fatal("normalized transcript revision accepted UPDATE")
	}
	if _, err := committer.CommitRaw(ctx, params); !errors.Is(err, job.ErrLeaseConflict) {
		t.Fatalf("replayed CommitRaw() error = %v, want ErrLeaseConflict", err)
	}
}

func equalJSON(left, right []byte) bool {
	var leftValue any
	var rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil &&
		reflect.DeepEqual(leftValue, rightValue)
}

func migratedTranscriptionPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := fmt.Sprintf("transcription_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema")
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create pool")
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire migration connection")
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
