package job_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	jobWorkspaceID = "10000000-0000-4000-8000-000000000031"
	jobOtherSpace  = "10000000-0000-4000-8000-000000000032"
	jobUserID      = "20000000-0000-4000-8000-000000000031"
	jobAssetID     = "30000000-0000-4000-8000-000000000031"
	jobOtherAsset  = "30000000-0000-4000-8000-000000000032"
	jobNoOriginal  = "30000000-0000-4000-8000-000000000033"
	jobOriginalID  = "40000000-0000-4000-8000-000000000031"
	jobSHA256      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	jobRequestHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestPostgresRepositoryCreatesScopedIdempotentTranscription(t *testing.T) {
	pool := migratedJobPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	seedJobAccountsAndAssets(t, ctx, pool)
	repository := job.NewPostgresRepository(pool)
	params := job.CreateTranscriptionParams{
		JobID: "50000000-0000-4000-8000-000000000031", AuditID: "60000000-0000-4000-8000-000000000031",
		WorkspaceID: jobWorkspaceID, AssetID: jobAssetID, CreatedBy: jobUserID,
		Kind: job.KindMockTranscribe, Payload: []byte(`{"asset_id":"` + jobAssetID + `"}`),
		MaxAttempts: job.DefaultMaxAttempts, IdempotencyKey: "transcribe-1", RequestHash: jobRequestHash,
	}

	created, replayed, err := repository.CreateTranscription(ctx, params)
	if err != nil || replayed {
		t.Fatalf("CreateTranscription() = (%+v, %t, %v)", created, replayed, err)
	}
	if created.ID != params.JobID || created.State != job.StateQueued || created.Kind != job.KindMockTranscribe {
		t.Fatalf("created job = %+v", created)
	}
	params.JobID = "50000000-0000-4000-8000-000000000099"
	params.AuditID = "60000000-0000-4000-8000-000000000099"
	replayedJob, replayed, err := repository.CreateTranscription(ctx, params)
	if err != nil || !replayed || replayedJob.ID != created.ID {
		t.Fatalf("replayed CreateTranscription() = (%+v, %t, %v)", replayedJob, replayed, err)
	}
	params.RequestHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if _, _, err := repository.CreateTranscription(ctx, params); !errors.Is(err, job.ErrIdempotencyConflict) {
		t.Fatalf("conflicting CreateTranscription() error = %v", err)
	}
	if _, err := repository.Get(ctx, jobOtherSpace, created.ID); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("cross-workspace Get() error = %v, want ErrNotFound", err)
	}

	params.JobID = "50000000-0000-4000-8000-000000000033"
	params.AuditID = "60000000-0000-4000-8000-000000000033"
	params.AssetID = jobNoOriginal
	params.IdempotencyKey = "transcribe-no-original"
	params.RequestHash = jobRequestHash
	if _, _, err := repository.CreateTranscription(ctx, params); !errors.Is(err, job.ErrAssetNotReady) {
		t.Fatalf("asset without original error = %v, want ErrAssetNotReady", err)
	}
	params.AssetID = jobOtherAsset
	params.IdempotencyKey = "transcribe-cross-workspace"
	if _, _, err := repository.CreateTranscription(ctx, params); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("cross-workspace asset error = %v, want ErrNotFound", err)
	}

	var status string
	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT a.status,
		       (SELECT count(*) FROM audit_logs WHERE action = 'transcription.requested')
		FROM assets a WHERE a.id = $1`, jobAssetID).Scan(&status, &auditCount); err != nil {
		t.Fatalf("query create side effects: %v", err)
	}
	if status != "processing" || auditCount != 1 {
		t.Fatalf("create side effects = status %q / audits %d", status, auditCount)
	}
}

func TestPostgresRepositoryClaimLeaseAndCompletionLifecycle(t *testing.T) {
	pool := migratedJobPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	seedJobAccountsAndAssets(t, ctx, pool)
	repository := job.NewPostgresRepository(pool)
	created := createJob(t, ctx, repository, jobAssetID, "50000000-0000-4000-8000-000000000041", "transcribe-claim")
	now := time.Now().UTC().Add(time.Minute)

	start := make(chan struct{})
	results := make(chan claimResult, 2)
	var workers sync.WaitGroup
	for _, workerID := range []string{"worker-a", "worker-b"} {
		workers.Add(1)
		go func(workerID string) {
			defer workers.Done()
			<-start
			claimed, err := repository.Claim(ctx, job.ClaimParams{
				Kind: job.KindMockTranscribe, WorkerID: workerID,
				Now: now, LeaseDuration: time.Minute,
			})
			results <- claimResult{job: claimed, err: err}
		}(workerID)
	}
	close(start)
	workers.Wait()
	close(results)
	var claimed job.Job
	var successCount, emptyCount int
	for result := range results {
		switch {
		case result.err == nil:
			successCount++
			claimed = result.job
		case errors.Is(result.err, job.ErrNoClaimableJob):
			emptyCount++
		default:
			t.Fatalf("Claim() unexpected error = %v", result.err)
		}
	}
	if successCount != 1 || emptyCount != 1 || claimed.ID != created.ID || claimed.Attempts != 1 {
		t.Fatalf("concurrent claims = success %d empty %d job %+v", successCount, emptyCount, claimed)
	}
	completed, err := repository.Succeed(ctx, job.SucceedParams{
		JobID: claimed.ID, WorkerID: *claimed.LeaseOwner, Now: now.Add(10 * time.Second),
	})
	if err != nil || completed.State != job.StateSucceeded || completed.LeaseOwner != nil {
		t.Fatalf("Succeed() = (%+v, %v)", completed, err)
	}
	var succeededOutcome string
	if err := pool.QueryRow(ctx, `
		SELECT outcome FROM job_attempts WHERE job_id = $1 AND attempt = 1`, created.ID,
	).Scan(&succeededOutcome); err != nil || succeededOutcome != "succeeded" {
		t.Fatalf("succeeded attempt outcome = %q, %v", succeededOutcome, err)
	}

	secondAsset := "30000000-0000-4000-8000-000000000034"
	seedReadyAssetWithOriginal(t, ctx, pool, secondAsset, "40000000-0000-4000-8000-000000000034")
	second := createJob(t, ctx, repository, secondAsset, "50000000-0000-4000-8000-000000000044", "transcribe-retry")
	firstLease, err := repository.Claim(ctx, job.ClaimParams{
		Kind: job.KindMockTranscribe, WorkerID: "crashed-worker", Now: now, LeaseDuration: time.Second,
	})
	if err != nil || firstLease.ID != second.ID {
		t.Fatalf("first retry Claim() = (%+v, %v)", firstLease, err)
	}
	reclaimed, err := repository.Claim(ctx, job.ClaimParams{
		Kind: job.KindMockTranscribe, WorkerID: "replacement-worker",
		Now: now.Add(2 * time.Second), LeaseDuration: time.Minute,
	})
	if err != nil || reclaimed.ID != second.ID || reclaimed.Attempts != 2 || *reclaimed.LeaseOwner != "replacement-worker" {
		t.Fatalf("expired lease Claim() = (%+v, %v)", reclaimed, err)
	}
	var expiredOutcome string
	if err := pool.QueryRow(ctx, `
		SELECT outcome FROM job_attempts WHERE job_id = $1 AND attempt = 1`, second.ID,
	).Scan(&expiredOutcome); err != nil || expiredOutcome != "lease_expired" {
		t.Fatalf("expired attempt outcome = %q, %v", expiredOutcome, err)
	}

	if _, err := repository.Fail(ctx, job.FailParams{
		JobID: second.ID, WorkerID: "replacement-worker", ErrorCode: "password=secret",
		Now: now.Add(3 * time.Second), RetryAt: now.Add(time.Minute),
	}); !errors.Is(err, job.ErrInvalidErrorCode) {
		t.Fatalf("unsafe Fail() error = %v, want ErrInvalidErrorCode", err)
	}
	retrying, err := repository.Fail(ctx, job.FailParams{
		JobID: second.ID, WorkerID: "replacement-worker", ErrorCode: job.ErrorCodeProviderUnavailable,
		Now: now.Add(3 * time.Second), RetryAt: now.Add(time.Minute),
	})
	if err != nil || retrying.State != job.StateRetryWait || retrying.LeaseOwner != nil {
		t.Fatalf("retrying Fail() = (%+v, %v)", retrying, err)
	}
	if _, err := repository.Claim(ctx, job.ClaimParams{
		Kind: job.KindMockTranscribe, WorkerID: "too-early", Now: now.Add(30 * time.Second), LeaseDuration: time.Minute,
	}); !errors.Is(err, job.ErrNoClaimableJob) {
		t.Fatalf("early Claim() error = %v, want ErrNoClaimableJob", err)
	}
	thirdAttempt, err := repository.Claim(ctx, job.ClaimParams{
		Kind: job.KindMockTranscribe, WorkerID: "final-worker", Now: now.Add(time.Minute), LeaseDuration: time.Minute,
	})
	if err != nil || thirdAttempt.Attempts != 3 {
		t.Fatalf("third Claim() = (%+v, %v)", thirdAttempt, err)
	}
	failed, err := repository.Fail(ctx, job.FailParams{
		JobID: second.ID, WorkerID: "final-worker", ErrorCode: job.ErrorCodeInvalidAudio,
		Now: now.Add(61 * time.Second), RetryAt: now.Add(2 * time.Minute),
	})
	if err != nil || failed.State != job.StateFailed || failed.LeaseOwner != nil {
		t.Fatalf("terminal Fail() = (%+v, %v)", failed, err)
	}
	if failed.LastErrorCode == nil || *failed.LastErrorCode != job.ErrorCodeInvalidAudio {
		t.Fatalf("terminal error code = %v", failed.LastErrorCode)
	}
	var failedAssetState string
	if err := pool.QueryRow(ctx, "SELECT status FROM assets WHERE id = $1", secondAsset).Scan(&failedAssetState); err != nil {
		t.Fatalf("query terminal asset state")
	}
	if failedAssetState != "failed" {
		t.Fatalf("terminal asset state = %q, want failed", failedAssetState)
	}
}

type claimResult struct {
	job job.Job
	err error
}

func createJob(
	t *testing.T,
	ctx context.Context,
	repository *job.PostgresRepository,
	assetID, jobID, key string,
) job.Job {
	t.Helper()
	created, replayed, err := repository.CreateTranscription(ctx, job.CreateTranscriptionParams{
		JobID: jobID, AuditID: "60000000-0000-4000-8000-" + jobID[len(jobID)-12:],
		WorkspaceID: jobWorkspaceID, AssetID: assetID, CreatedBy: jobUserID,
		Kind: job.KindMockTranscribe, Payload: []byte(`{"asset_id":"` + assetID + `"}`),
		MaxAttempts: job.DefaultMaxAttempts, IdempotencyKey: key, RequestHash: jobRequestHash,
	})
	if err != nil || replayed {
		t.Fatalf("create job = (%+v, %t, %v)", created, replayed, err)
	}
	return created
}

func seedJobAccountsAndAssets(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "INSERT INTO workspaces (id, name) VALUES ($1, 'Primary'), ($2, 'Other')", jobWorkspaceID, jobOtherSpace); err != nil {
		t.Fatalf("seed workspaces")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, status)
		VALUES ($1, 'job-owner@example.com', 'encoded', 'active')`, jobUserID); err != nil {
		t.Fatalf("seed user")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memberships (workspace_id, user_id, role)
		VALUES ($1, $3, 'owner'), ($2, $3, 'owner')`, jobWorkspaceID, jobOtherSpace, jobUserID); err != nil {
		t.Fatalf("seed memberships")
	}
	seedReadyAssetWithOriginal(t, ctx, pool, jobAssetID, jobOriginalID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets (id, workspace_id, title, language, status, created_by)
		VALUES ($1, $2, 'Other asset', 'en', 'ready', $3),
		       ($4, $5, 'Missing original', 'en', 'ready', $3)`,
		jobOtherAsset, jobOtherSpace, jobUserID, jobNoOriginal, jobWorkspaceID,
	); err != nil {
		t.Fatalf("seed scoped assets")
	}
}

func seedReadyAssetWithOriginal(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	assetID, objectID string,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO assets (id, workspace_id, title, language, status, created_by)
		VALUES ($1, $2, 'Ready asset', 'en', 'ready', $3)`,
		assetID, jobWorkspaceID, jobUserID,
	); err != nil {
		t.Fatalf("seed ready asset")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			file_size, sha256, creation_source, encryption_state
		) VALUES ($1, $2, 'original', 'local', $3, 'audio/wav', 1024, $4, 'upload', 'none')`,
		objectID, assetID, "objects/"+assetID+"/original", jobSHA256,
	); err != nil {
		t.Fatalf("seed original object")
	}
}

func migratedJobPool(t *testing.T) *pgxpool.Pool {
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
	schema := fmt.Sprintf("job_test_%d", time.Now().UnixNano())
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
