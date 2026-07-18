package transcript_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/transcript"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryReadsTimelineWithinWorkspace(t *testing.T) {
	pool := migratedTranscriptPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const (
		workspaceID  = "10000000-0000-4000-8000-000000000041"
		otherSpace   = "10000000-0000-4000-8000-000000000042"
		userID       = "20000000-0000-4000-8000-000000000041"
		assetID      = "30000000-0000-4000-8000-000000000041"
		transcriptID = "40000000-0000-4000-8000-000000000041"
		revisionID   = "50000000-0000-4000-8000-000000000041"
		segmentID    = "60000000-0000-4000-8000-000000000041"
		jobID        = "70000000-0000-4000-8000-000000000041"
		rawObjectID  = "80000000-0000-4000-8000-000000000041"
	)
	seedStatements := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')", []any{workspaceID, otherSpace}},
		{"INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'owner@example.com', 'encoded', 'active')", []any{userID}},
		{"INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $3, 'owner'), ($2, $3, 'owner')", []any{workspaceID, otherSpace, userID}},
		{"INSERT INTO assets (id, workspace_id, title, language, status, created_by) VALUES ($1, $2, 'Recording', 'en-US', 'ready', $3)", []any{assetID, workspaceID, userID}},
		{`INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			file_size, sha256, creation_source, encryption_state
		) VALUES ($1, $2, 'provider_raw_response', 'local', 'objects/raw.json',
			'application/json', 2, repeat('a', 64), 'mock_asr', 'none')`, []any{rawObjectID, assetID}},
		{"INSERT INTO jobs (id, workspace_id, asset_id, created_by, kind, state) VALUES ($1, $2, $3, $4, 'mock_transcribe', 'succeeded')", []any{jobID, workspaceID, assetID, userID}},
		{"INSERT INTO transcripts (id, asset_id, language) VALUES ($1, $2, 'en-US')", []any{transcriptID, assetID}},
		{`INSERT INTO transcript_revisions (
			id, transcript_id, kind, text_content, provider_snapshot, hotword_snapshot,
			source_job_id, provider_raw_object_id, created_by
		) VALUES ($1, $2, 'raw_asr', 'Welcome to VoiceAsset.',
			'{"provider_id":"mock_asr","version":"1"}',
			'{"sets":[{"id":"set-1","version":1}]}', $3, $4, $5)`, []any{revisionID, transcriptID, jobID, rawObjectID, userID}},
		{`INSERT INTO transcript_segments (
			id, revision_id, ordinal, start_ms, end_ms, text_content, confidence, words
		) VALUES ($1, $2, 0, 0, 1200, 'Welcome', 0.98,
			'[{"text":"Welcome","start_ms":0,"end_ms":700}]')`, []any{segmentID, revisionID}},
	}
	for _, statement := range seedStatements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed transcript fixture")
		}
	}

	repository := transcript.NewPostgresRepository(pool)
	summaries, err := repository.List(ctx, workspaceID, assetID)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(summaries) != 1 || summaries[0].ID != transcriptID ||
		summaries[0].LatestRevisionID != revisionID || summaries[0].LatestText != "Welcome to VoiceAsset." {
		t.Fatalf("List() = %+v", summaries)
	}
	revision, err := repository.GetRevision(ctx, workspaceID, revisionID)
	if err != nil {
		t.Fatalf("GetRevision() error = %v", err)
	}
	if revision.AssetID != assetID || revision.ProviderRawObjectID != rawObjectID ||
		string(revision.HotwordSnapshot) != `{"sets": [{"id": "set-1", "version": 1}]}` ||
		len(revision.Segments) != 1 || revision.Segments[0].StartMS != 0 || revision.Segments[0].EndMS != 1200 {
		t.Fatalf("GetRevision() = %+v", revision)
	}
	if _, err := repository.List(ctx, otherSpace, assetID); !errors.Is(err, transcript.ErrNotFound) {
		t.Fatalf("cross-workspace List() error = %v, want ErrNotFound", err)
	}
	if _, err := repository.GetRevision(ctx, otherSpace, revisionID); !errors.Is(err, transcript.ErrNotFound) {
		t.Fatalf("cross-workspace GetRevision() error = %v, want ErrNotFound", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE transcript_segments SET text_content = 'tampered' WHERE id = $1", segmentID); err == nil {
		t.Fatal("immutable transcript segment accepted UPDATE")
	}
}

func migratedTranscriptPool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("transcript_test_%d", time.Now().UnixNano())
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
