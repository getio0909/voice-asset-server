package assetpurge_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/assetpurge"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	purgeWorkspaceID = "10000000-0000-4000-8000-0000000000a1"
	purgeUserID      = "20000000-0000-4000-8000-0000000000a1"
	purgeAssetID     = "30000000-0000-4000-8000-0000000000a1"
	purgeObjectID    = "40000000-0000-4000-8000-0000000000a1"
	purgeUploadID    = "50000000-0000-4000-8000-0000000000a1"
	purgePriorJobID  = "60000000-0000-4000-8000-0000000000a1"
	purgeTranscript  = "70000000-0000-4000-8000-0000000000a1"
	purgeRevision    = "80000000-0000-4000-8000-0000000000a1"
	purgeSegment     = "90000000-0000-4000-8000-0000000000a1"
	purgeReview      = "a0000000-0000-4000-8000-0000000000a1"
	purgeTagID       = "b0000000-0000-4000-8000-0000000000a1"
	purgeAnnotation  = "c0000000-0000-4000-8000-0000000000a1"
	purgeHotword     = "d0000000-0000-4000-8000-0000000000a1"
	purgeHotwordVer  = "d0000000-0000-4000-8000-0000000000a2"
	purgeGlossary    = "e0000000-0000-4000-8000-0000000000a1"
	purgeGlossaryVer = "e0000000-0000-4000-8000-0000000000a2"
)

func TestPermanentAssetPurgeRemovesBytesAndRelationalGraphButRetainsAudits(t *testing.T) {
	pool := migratedPurgePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal() error = %v", err)
	}
	content := []byte("permanently deleted source audio")
	digest := sha256.Sum256(content)
	sha := hex.EncodeToString(digest[:])
	part, err := store.PutPart(ctx, purgeUploadID, 1, bytes.NewReader(content), storage.PutPartOptions{
		ExpectedSize: int64(len(content)), ExpectedSHA256: sha, MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("PutPart() error = %v", err)
	}
	object, err := store.Assemble(ctx, purgeAssetID, purgeUploadID, []storage.PartRef{part.Ref()}, storage.AssembleOptions{
		ExpectedSize: int64(len(content)), ExpectedSHA256: sha, MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	seedPurgeGraph(t, ctx, pool, part, object)

	assetService := asset.NewService(asset.NewPostgresRepository(pool))
	principal := auth.Principal{
		UserID: purgeUserID, WorkspaceID: purgeWorkspaceID, Role: "owner",
		Scopes: []string{auth.ScopeAssetsWrite},
	}
	trashed, err := assetService.Trash(ctx, principal, purgeAssetID, 1, "purge-trash")
	if err != nil || trashed.Status != "trashed" || trashed.Version != 2 {
		t.Fatalf("Trash() = (%+v, %v)", trashed, err)
	}
	request, replayed, err := assetService.RequestPurge(
		ctx, principal, purgeAssetID, trashed.Version,
		asset.PurgeInput{Confirmation: purgeAssetID}, "purge-idempotency", "purge-request",
	)
	if err != nil || replayed || request.State != job.StateQueued || request.AssetVersion != 3 {
		t.Fatalf("RequestPurge() = (%+v, %v, %v)", request, replayed, err)
	}

	jobRepository := job.NewPostgresRepository(pool)
	processor := assetpurge.NewProcessor(
		jobRepository, assetpurge.NewPostgresRepository(pool), store, "purge-worker",
	)
	processed, err := processor.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("RunOnce() = (%v, %v)", processed, err)
	}
	if file, openErr := store.Open(ctx, object.Key); openErr == nil {
		_ = file.Close()
		t.Fatal("purged original object still exists")
	}
	if file, openErr := store.Open(ctx, part.Key); openErr == nil {
		_ = file.Close()
		t.Fatal("purged upload part still exists")
	}

	for name, query := range map[string]string{
		"asset":            `SELECT count(*) FROM assets WHERE id = $1`,
		"objects":          `SELECT count(*) FROM asset_objects WHERE asset_id = $1`,
		"uploads":          `SELECT count(*) FROM upload_sessions WHERE asset_id = $1`,
		"transcripts":      `SELECT count(*) FROM transcripts WHERE asset_id = $1`,
		"tags":             `SELECT count(*) FROM asset_tags WHERE asset_id = $1`,
		"annotations":      `SELECT count(*) FROM annotations WHERE asset_id = $1`,
		"asset hotwords":   `SELECT count(*) FROM hotword_sets WHERE scope_type = 'asset' AND scope_id = $1`,
		"asset glossaries": `SELECT count(*) FROM glossary_sets WHERE scope_type = 'asset' AND scope_id = $1`,
		"prior asset jobs": `SELECT count(*) FROM jobs WHERE asset_id = $1`,
	} {
		var count int
		if err := pool.QueryRow(ctx, query, purgeAssetID).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", name, count)
		}
	}
	var purgeState string
	var purgeAssetReference *string
	if err := pool.QueryRow(ctx, `
		SELECT state, asset_id::text FROM jobs WHERE id = $1`, request.JobID,
	).Scan(&purgeState, &purgeAssetReference); err != nil {
		t.Fatalf("query completed purge job: %v", err)
	}
	if purgeState != job.StateSucceeded || purgeAssetReference != nil {
		t.Fatalf("purge job state/reference = %q/%v", purgeState, purgeAssetReference)
	}
	var requestedAudits, completedAudits int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE action = 'asset.purge_requested'),
		       count(*) FILTER (WHERE action = 'asset.purged')
		FROM audit_logs WHERE target_id = $1`, purgeAssetID,
	).Scan(&requestedAudits, &completedAudits); err != nil {
		t.Fatalf("count purge audits: %v", err)
	}
	if requestedAudits != 1 || completedAudits != 1 {
		t.Fatalf("purge audits requested/completed = %d/%d", requestedAudits, completedAudits)
	}

	replayedRequest, replayed, err := assetService.RequestPurge(
		ctx, principal, purgeAssetID, trashed.Version,
		asset.PurgeInput{Confirmation: purgeAssetID}, "purge-idempotency", "purge-replay",
	)
	if err != nil || !replayed || replayedRequest.JobID != request.JobID ||
		replayedRequest.State != job.StateSucceeded || replayedRequest.AssetVersion != request.AssetVersion {
		t.Fatalf("replayed RequestPurge() = (%+v, %v, %v)", replayedRequest, replayed, err)
	}
	got, err := assetService.GetPurge(ctx, principal, request.JobID)
	if err != nil || got != replayedRequest {
		t.Fatalf("GetPurge(completed) = (%+v, %v), want %+v", got, err, replayedRequest)
	}
}

func seedPurgeGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, part storage.Part, object storage.Object) {
	t.Helper()
	statements := []struct {
		name  string
		query string
		args  []any
	}{
		{"workspace", `INSERT INTO workspaces (id, name) VALUES ($1, 'Purge workspace')`, []any{purgeWorkspaceID}},
		{"user", `INSERT INTO users (id, email, password_hash, status) VALUES ($1, 'purge@example.com', 'encoded', 'active')`, []any{purgeUserID}},
		{"membership", `INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, []any{purgeWorkspaceID, purgeUserID}},
		{"asset", `INSERT INTO assets (id, workspace_id, title, language, status, created_by) VALUES ($1, $2, 'Delete me', 'en', 'ready', $3)`, []any{purgeAssetID, purgeWorkspaceID, purgeUserID}},
		{"original", `INSERT INTO asset_objects (id, asset_id, kind, storage_backend, storage_key, mime_type, file_size, sha256, creation_source, encryption_state) VALUES ($1, $2, 'original', $3, $4, 'audio/wav', $5, $6, 'upload', 'none')`, []any{purgeObjectID, purgeAssetID, object.Backend, object.Key, object.Size, object.SHA256}},
		{"upload", `INSERT INTO upload_sessions (id, workspace_id, asset_id, created_by, filename, mime_type, expected_size, expected_sha256, part_size, idempotency_key, idempotency_request_hash, state, expires_at, completed_at) VALUES ($1, $2, $3, $4, 'source.wav', 'audio/wav', $5, $6, 65536, 'upload-purge', repeat('a', 64), 'completed', clock_timestamp() + interval '1 hour', clock_timestamp())`, []any{purgeUploadID, purgeWorkspaceID, purgeAssetID, purgeUserID, object.Size, object.SHA256}},
		{"part", `INSERT INTO upload_parts (upload_session_id, part_number, storage_key, size_bytes, sha256) VALUES ($1, 1, $2, $3, $4)`, []any{purgeUploadID, part.Key, part.Size, part.SHA256}},
		{"prior job", `INSERT INTO jobs (id, workspace_id, asset_id, created_by, kind, state, payload, max_attempts) VALUES ($1, $2, $3, $4, 'mock_transcribe', 'succeeded', '{}', 3)`, []any{purgePriorJobID, purgeWorkspaceID, purgeAssetID, purgeUserID}},
		{"transcript", `INSERT INTO transcripts (id, asset_id, language) VALUES ($1, $2, 'en')`, []any{purgeTranscript, purgeAssetID}},
		{"revision", `INSERT INTO transcript_revisions (id, transcript_id, kind, text_content, created_by, source_job_id) VALUES ($1, $2, 'raw_asr', 'private transcript', $3, $4)`, []any{purgeRevision, purgeTranscript, purgeUserID, purgePriorJobID}},
		{"job result", `UPDATE jobs SET result_revision_id = $2 WHERE id = $1`, []any{purgePriorJobID, purgeRevision}},
		{"segment", `INSERT INTO transcript_segments (id, revision_id, ordinal, start_ms, end_ms, text_content) VALUES ($1, $2, 0, 0, 1000, 'private transcript')`, []any{purgeSegment, purgeRevision}},
		{"review", `INSERT INTO transcript_revision_reviews (id, workspace_id, revision_id, reviewer_id, action) VALUES ($1, $2, $3, $4, 'reject_all')`, []any{purgeReview, purgeWorkspaceID, purgeRevision, purgeUserID}},
		{"tag", `INSERT INTO tags (id, workspace_id, name, created_by) VALUES ($1, $2, 'private', $3)`, []any{purgeTagID, purgeWorkspaceID, purgeUserID}},
		{"asset tag", `INSERT INTO asset_tags (workspace_id, asset_id, tag_id, created_by) VALUES ($1, $2, $3, $4)`, []any{purgeWorkspaceID, purgeAssetID, purgeTagID, purgeUserID}},
		{"annotation", `INSERT INTO annotations (id, workspace_id, asset_id, kind, start_ms, body, created_by) VALUES ($1, $2, $3, 'note', 0, 'private note', $4)`, []any{purgeAnnotation, purgeWorkspaceID, purgeAssetID, purgeUserID}},
		{"hotword", `INSERT INTO hotword_sets (id, workspace_id, display_name, scope_type, scope_id, state, created_by) VALUES ($1, $2, 'Asset hotwords', 'asset', $3, 'enabled', $4)`, []any{purgeHotword, purgeWorkspaceID, purgeAssetID, purgeUserID}},
		{"hotword version", `INSERT INTO hotword_set_versions (id, hotword_set_id, version, entries, created_by) VALUES ($1, $2, 1, '[{"term":"private","weight":1}]', $3)`, []any{purgeHotwordVer, purgeHotword, purgeUserID}},
		{"glossary", `INSERT INTO glossary_sets (id, workspace_id, display_name, scope_type, scope_id, state, created_by) VALUES ($1, $2, 'Asset glossary', 'asset', $3, 'enabled', $4)`, []any{purgeGlossary, purgeWorkspaceID, purgeAssetID, purgeUserID}},
		{"glossary version", `INSERT INTO glossary_set_versions (id, glossary_set_id, version, entries, created_by) VALUES ($1, $2, 1, '[{"canonical_form":"private","aliases":["secret"]}]', $3)`, []any{purgeGlossaryVer, purgeGlossary, purgeUserID}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed %s: %v", statement.name, err)
		}
	}
}

func migratedPurgePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	schema := fmt.Sprintf("asset_purge_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := conn.Exec(ctx, "SET search_path TO "+identifier); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	files, err := migration.Load(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if _, err := migration.Apply(ctx, conn, files); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = conn.Exec(context.Background(), "SET search_path TO public")
		_, _ = conn.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE")
		_ = conn.Close(context.Background())
	})
	return pool
}

func TestPurgeRejectsWrongObjectIntegrityWithoutDeletingMetadata(t *testing.T) {
	pool := migratedPurgePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal() error = %v", err)
	}
	content := []byte("integrity protected")
	digest := sha256.Sum256(content)
	sha := hex.EncodeToString(digest[:])
	part, err := store.PutPart(ctx, purgeUploadID, 1, bytes.NewReader(content), storage.PutPartOptions{
		ExpectedSize: int64(len(content)), ExpectedSHA256: sha, MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("PutPart() error = %v", err)
	}
	object, err := store.Assemble(ctx, purgeAssetID, purgeUploadID, []storage.PartRef{part.Ref()}, storage.AssembleOptions{
		ExpectedSize: int64(len(content)), ExpectedSHA256: sha, MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	seedPurgeGraph(t, ctx, pool, part, object)
	file, err := store.Open(ctx, object.Key)
	if err != nil {
		t.Fatalf("open object for tamper path: %v", err)
	}
	objectPath := file.Name()
	if err := file.Close(); err != nil {
		t.Fatalf("close object before tamper: %v", err)
	}
	if err := os.WriteFile(objectPath, []byte("tampered bytes"), 0o600); err != nil {
		t.Fatalf("tamper object bytes: %v", err)
	}
	service := asset.NewService(asset.NewPostgresRepository(pool))
	principal := auth.Principal{UserID: purgeUserID, WorkspaceID: purgeWorkspaceID, Role: "owner", Scopes: []string{auth.ScopeAssetsWrite}}
	trashed, err := service.Trash(ctx, principal, purgeAssetID, 1, "tamper-trash")
	if err != nil {
		t.Fatalf("Trash() error = %v", err)
	}
	request, _, err := service.RequestPurge(ctx, principal, purgeAssetID, trashed.Version, asset.PurgeInput{Confirmation: purgeAssetID}, "tamper-purge", "tamper-request")
	if err != nil {
		t.Fatalf("RequestPurge() error = %v", err)
	}
	processor := assetpurge.NewProcessor(job.NewPostgresRepository(pool), assetpurge.NewPostgresRepository(pool), store, "purge-worker")
	processed, err := processor.RunOnce(ctx)
	if !processed || !errors.Is(err, assetpurge.ErrProcessingFailed) {
		t.Fatalf("RunOnce() = (%v, %v)", processed, err)
	}
	var assetStatus, purgeState string
	if err := pool.QueryRow(ctx, `SELECT status FROM assets WHERE id = $1`, purgeAssetID).Scan(&assetStatus); err != nil {
		t.Fatalf("query retained asset: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT state FROM jobs WHERE id = $1`, request.JobID).Scan(&purgeState); err != nil {
		t.Fatalf("query retrying purge job: %v", err)
	}
	if assetStatus != "purging" || purgeState != job.StateRetryWait {
		t.Fatalf("retained state asset/job = %q/%q", assetStatus, purgeState)
	}
	if _, _, err := service.RequestPurge(
		ctx, principal, purgeAssetID, 3, asset.PurgeInput{Confirmation: purgeAssetID},
		"tamper-purge-active", "tamper-active-request",
	); !errors.Is(err, asset.ErrPurgeNotEligible) {
		t.Fatalf("RequestPurge(active retry) error = %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE jobs SET state = 'failed', available_at = clock_timestamp()
		WHERE id = $1 AND state = 'retry_wait'`, request.JobID,
	); err != nil {
		t.Fatalf("make purge terminal for resume test: %v", err)
	}
	resumed, replayed, err := service.RequestPurge(
		ctx, principal, purgeAssetID, 3, asset.PurgeInput{Confirmation: purgeAssetID},
		"tamper-purge-resume", "tamper-resume-request",
	)
	if err != nil || replayed || resumed.State != job.StateQueued || resumed.AssetVersion != 3 {
		t.Fatalf("RequestPurge(resume) = (%+v, %v, %v)", resumed, replayed, err)
	}
	var resumeAuditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE target_id = $1 AND action = 'asset.purge_resumed'`, purgeAssetID,
	).Scan(&resumeAuditCount); err != nil {
		t.Fatalf("count purge resume audit: %v", err)
	}
	if resumeAuditCount != 1 {
		t.Fatalf("purge resume audit count = %d", resumeAuditCount)
	}
	if file, openErr := store.Open(ctx, object.Key); openErr != nil {
		t.Fatalf("integrity mismatch removed object: %v", openErr)
	} else {
		_ = file.Close()
	}
	if err := os.WriteFile(objectPath, content, 0o600); err != nil {
		t.Fatalf("restore expected object bytes for resumed purge: %v", err)
	}
	processed, err = processor.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("RunOnce(resumed) = (%v, %v)", processed, err)
	}
	completed, err := service.GetPurge(ctx, principal, resumed.JobID)
	if err != nil || completed.State != job.StateSucceeded || completed.AssetVersion != 3 ||
		completed.AssetID != purgeAssetID {
		t.Fatalf("GetPurge(resumed completion) = (%+v, %v)", completed, err)
	}
}
