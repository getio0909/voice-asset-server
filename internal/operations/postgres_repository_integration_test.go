package operations_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/operations"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	operationsWorkspace    = "10000000-0000-4000-8000-000000000081"
	operationsOtherSpace   = "10000000-0000-4000-8000-000000000082"
	operationsUser         = "20000000-0000-4000-8000-000000000081"
	operationsOtherUser    = "20000000-0000-4000-8000-000000000082"
	operationsAsset        = "30000000-0000-4000-8000-000000000081"
	operationsTrashedAsset = "30000000-0000-4000-8000-000000000082"
	operationsOtherAsset   = "30000000-0000-4000-8000-000000000083"
	operationsNewestJob    = "40000000-0000-4000-8000-000000000084"
	operationsOlderJob     = "40000000-0000-4000-8000-000000000083"
	operationsNewestAudit  = "50000000-0000-4000-8000-000000000084"
	operationsOlderAudit   = "50000000-0000-4000-8000-000000000083"
)

func TestPostgresOperationsReadsArePaginatedAndWorkspaceScoped(t *testing.T) {
	pool := migratedOperationsPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	seedOperationsFixtures(t, ctx, pool)

	service := operations.NewService(operations.NewPostgresRepository(pool))
	principal := auth.Principal{WorkspaceID: operationsWorkspace, Scopes: []string{auth.ScopeAdminRead}}
	jobs, err := service.ListJobs(ctx, principal, operations.JobListInput{
		Limit: 1, State: "failed", Kind: "mock_transcribe",
	})
	if err != nil || len(jobs.Items) != 1 || jobs.Items[0].ID != operationsNewestJob || jobs.NextCursor == nil {
		t.Fatalf("ListJobs(first) = (%+v, %v)", jobs, err)
	}
	jobs, err = service.ListJobs(ctx, principal, operations.JobListInput{
		Limit: 1, State: "failed", Kind: "mock_transcribe", Cursor: *jobs.NextCursor,
	})
	if err != nil || len(jobs.Items) != 1 || jobs.Items[0].ID != operationsOlderJob || jobs.NextCursor != nil {
		t.Fatalf("ListJobs(second) = (%+v, %v)", jobs, err)
	}

	audits, err := service.ListAuditLogs(ctx, principal, operations.AuditListInput{
		Limit: 1, ActorType: "user", Action: "asset.read", TargetType: "asset",
	})
	if err != nil || len(audits.Items) != 1 || audits.Items[0].ID != operationsNewestAudit ||
		audits.Items[0].ActorEmail != "owner@example.test" || audits.NextCursor == nil {
		t.Fatalf("ListAuditLogs(first) = (%+v, %v)", audits, err)
	}
	audits, err = service.ListAuditLogs(ctx, principal, operations.AuditListInput{
		Limit: 1, ActorType: "user", Action: "asset.read", TargetType: "asset", Cursor: *audits.NextCursor,
	})
	if err != nil || len(audits.Items) != 1 || audits.Items[0].ID != operationsOlderAudit || audits.NextCursor != nil {
		t.Fatalf("ListAuditLogs(second) = (%+v, %v)", audits, err)
	}

	status, err := service.GetSystemStatus(ctx, principal)
	if err != nil {
		t.Fatalf("GetSystemStatus() error = %v", err)
	}
	if status.ActiveUsers != 1 || status.Assets.Total != 2 || status.Assets.Active != 1 ||
		status.Assets.Trashed != 1 || status.Assets.AudioDurationMS != 10_000 ||
		status.Storage.ObjectCount != 3 || status.Storage.Bytes != 320 ||
		status.Transcripts.TranscriptCount != 1 || status.Transcripts.RevisionCount != 1 ||
		status.Jobs.Total != 4 || status.Jobs.Failed != 2 || status.Jobs.Succeeded != 1 ||
		status.Jobs.Queued != 1 || status.Providers.EnabledASR != 1 || status.Providers.EnabledLLM != 1 {
		t.Fatalf("system status = %+v", status)
	}
}

func TestPostgresOperationsRetryIsAtomicWorkspaceScopedAndBounded(t *testing.T) {
	pool := migratedOperationsPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	seedOperationsFixtures(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		UPDATE jobs
		SET attempts = 3, max_attempts = 3, last_error_code = 'provider_unavailable'
		WHERE id = $1;
		UPDATE assets SET status = 'failed' WHERE id = $2`, operationsNewestJob, operationsAsset); err != nil {
		t.Fatalf("prepare retry fixture: %v", err)
	}

	service := operations.NewService(operations.NewPostgresRepository(pool))
	principal := auth.Principal{
		UserID: operationsUser, WorkspaceID: operationsWorkspace,
		Scopes: []string{auth.ScopeAdminWrite},
	}
	before := time.Now().UTC()
	retried, err := service.RetryJob(ctx, principal, operationsNewestJob, "manual-retry")
	if err != nil {
		t.Fatalf("RetryJob() error = %v", err)
	}
	if retried.ID != operationsNewestJob || retried.State != "queued" || retried.Retryable ||
		retried.Attempts != 3 || retried.MaxAttempts != 4 || retried.LastErrorCode != nil ||
		retried.AvailableAt.Before(before) {
		t.Fatalf("retried job = %+v", retried)
	}
	var assetStatus, auditAction, auditRequestID string
	var auditMetadata map[string]any
	if err := pool.QueryRow(ctx, "SELECT status FROM assets WHERE id = $1", operationsAsset).Scan(&assetStatus); err != nil {
		t.Fatalf("query retried asset: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT action, request_id, metadata
		FROM audit_logs
		WHERE workspace_id = $1 AND target_id = $2 AND action = 'admin.job.retried'`,
		operationsWorkspace, operationsNewestJob,
	).Scan(&auditAction, &auditRequestID, &auditMetadata); err != nil {
		t.Fatalf("query retry audit: %v", err)
	}
	if assetStatus != "processing" || auditAction != "admin.job.retried" ||
		auditRequestID != "manual-retry" || auditMetadata["previous_state"] != "failed" {
		t.Fatalf("asset/audit = %q/%q/%q/%v", assetStatus, auditAction, auditRequestID, auditMetadata)
	}

	if _, err := service.RetryJob(ctx, principal, operationsNewestJob, "duplicate-retry"); !errors.Is(err, operations.ErrNotRetryable) {
		t.Fatalf("RetryJob(already queued) error = %v", err)
	}
	if _, err := service.RetryJob(ctx, principal, "40000000-0000-4000-8000-000000000085", "cross-workspace"); !errors.Is(err, operations.ErrNotFound) {
		t.Fatalf("RetryJob(other workspace) error = %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE jobs SET attempts = 20, max_attempts = 20 WHERE id = $1`, operationsOlderJob); err != nil {
		t.Fatalf("prepare exhausted fixture: %v", err)
	}
	if _, err := service.RetryJob(ctx, principal, operationsOlderJob, "exhausted-retry"); !errors.Is(err, operations.ErrRetryLimit) {
		t.Fatalf("RetryJob(exhausted) error = %v", err)
	}
}

func seedOperationsFixtures(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO workspaces (id, name) VALUES ($1, 'Operations'), ($2, 'Other');
		INSERT INTO users (id, email, password_hash, status) VALUES
			($3, 'owner@example.test', 'hash', 'active'),
			($4, 'other@example.test', 'hash', 'active');
		INSERT INTO memberships (workspace_id, user_id, role) VALUES
			($1, $3, 'owner'), ($2, $4, 'owner');
		INSERT INTO assets (id, workspace_id, title, status, duration_ms, created_by, deleted_at) VALUES
			($5, $1, 'Active', 'ready', 10000, $3, NULL),
			($6, $1, 'Trashed', 'trashed', 20000, $3, '2026-07-17T10:00:00Z'),
			($7, $2, 'Other', 'ready', 99999, $4, NULL);
		INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			file_size, sha256, creation_source, encryption_state
		) VALUES
			('60000000-0000-4000-8000-000000000081', $5, 'original', 'local', 'active.wav', 'audio/wav', 100, repeat('a', 64), 'upload', 'none'),
			('60000000-0000-4000-8000-000000000082', $5, 'waveform', 'local', 'active.png', 'image/png', 20, repeat('b', 64), 'worker', 'none'),
			('60000000-0000-4000-8000-000000000083', $6, 'original', 'local', 'trashed.wav', 'audio/wav', 200, repeat('c', 64), 'upload', 'none'),
			('60000000-0000-4000-8000-000000000084', $7, 'original', 'local', 'other.wav', 'audio/wav', 999, repeat('d', 64), 'upload', 'none');
		INSERT INTO transcripts (id, asset_id, language) VALUES
			('70000000-0000-4000-8000-000000000081', $5, 'en'),
			('70000000-0000-4000-8000-000000000082', $7, 'en');
		INSERT INTO transcript_revisions (id, transcript_id, kind, text_content, created_by) VALUES
			('71000000-0000-4000-8000-000000000081', '70000000-0000-4000-8000-000000000081', 'raw_asr', 'hello', $3),
			('71000000-0000-4000-8000-000000000082', '70000000-0000-4000-8000-000000000082', 'raw_asr', 'other', $4);
		INSERT INTO jobs (id, workspace_id, asset_id, created_by, kind, state, updated_at) VALUES
			('40000000-0000-4000-8000-000000000081', $1, $5, $3, 'generate_waveform', 'queued', '2026-07-17T09:00:00Z'),
			('40000000-0000-4000-8000-000000000082', $1, $5, $3, 'llm_correct', 'succeeded', '2026-07-17T10:00:00Z'),
			($8, $1, $5, $3, 'mock_transcribe', 'failed', '2026-07-17T11:00:00Z'),
			($9, $1, $5, $3, 'mock_transcribe', 'failed', '2026-07-17T12:00:00Z'),
			('40000000-0000-4000-8000-000000000085', $2, $7, $4, 'mock_transcribe', 'failed', '2026-07-17T13:00:00Z');
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata, occurred_at
		) VALUES
			('50000000-0000-4000-8000-000000000081', $1, $3, 'user', 'job.read', 'job', $8, 'job-read', '{}', '2026-07-17T09:00:00Z'),
			('50000000-0000-4000-8000-000000000082', $2, $4, 'user', 'asset.read', 'asset', $7, 'other-read', '{}', '2026-07-17T13:00:00Z'),
			($10, $1, $3, 'user', 'asset.read', 'asset', $5, 'older-read', '{"ordinal":1}', '2026-07-17T10:00:00Z'),
			($11, $1, $3, 'user', 'asset.read', 'asset', $5, 'newer-read', '{"ordinal":2}', '2026-07-17T11:00:00Z');
		INSERT INTO provider_profiles (
			id, workspace_id, provider_type, provider_id, display_name, state, created_by
		) VALUES
			('80000000-0000-4000-8000-000000000081', $1, 'asr', 'mock_asr', 'Mock ASR', 'enabled', $3),
			('80000000-0000-4000-8000-000000000082', $1, 'llm', 'mock_llm', 'Mock LLM', 'enabled', $3),
			('80000000-0000-4000-8000-000000000083', $1, 'asr', 'tencent', 'Disabled ASR', 'disabled', $3),
			('80000000-0000-4000-8000-000000000084', $2, 'asr', 'mock_asr', 'Other ASR', 'enabled', $4);`,
		operationsWorkspace, operationsOtherSpace, operationsUser, operationsOtherUser,
		operationsAsset, operationsTrashedAsset, operationsOtherAsset,
		operationsOlderJob, operationsNewestJob, operationsOlderAudit, operationsNewestAudit,
	)
	if err != nil {
		t.Fatalf("seed operations fixtures: %v", err)
	}
}

func migratedOperationsPool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("operations_test_%d", time.Now().UnixNano())
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
		t.Fatalf("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
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
