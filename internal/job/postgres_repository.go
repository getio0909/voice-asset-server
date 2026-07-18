package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (r *PostgresRepository) CreateCorrection(
	ctx context.Context,
	params CreateCorrectionParams,
) (Job, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Job{}, false, fmt.Errorf("begin correction job transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if existing, hash, err := loadByIdempotencyKey(ctx, tx, params.WorkspaceID, params.IdempotencyKey); err == nil {
		if hash != params.RequestHash {
			return Job{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, fmt.Errorf("load idempotent correction job: %w", err)
	}
	var assetID, revisionKind string
	var hasSegments bool
	err = tx.QueryRow(ctx, `
		SELECT transcript.asset_id::text, revision.kind,
		       EXISTS (SELECT 1 FROM transcript_segments WHERE revision_id = revision.id)
		FROM transcript_revisions revision
		JOIN transcripts transcript ON transcript.id = revision.transcript_id
		JOIN assets asset ON asset.id = transcript.asset_id
		WHERE revision.id = $1 AND asset.workspace_id = $2 AND asset.deleted_at IS NULL
		FOR KEY SHARE OF revision`, params.SourceRevisionID, params.WorkspaceID,
	).Scan(&assetID, &revisionKind, &hasSegments)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, ErrNotFound
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("validate correction source: %w", err)
	}
	if !hasSegments || !correctableRevisionKind(revisionKind) {
		return Job{}, false, ErrRevisionNotCorrectable
	}
	created, err := scanJob(tx.QueryRow(ctx, `
		INSERT INTO jobs (
			id, workspace_id, asset_id, created_by, kind, state, payload,
			max_attempts, idempotency_key, idempotency_request_hash
		) VALUES ($1, $2, $3, $4, $5, 'queued', $6, $7, $8, $9)
		ON CONFLICT (workspace_id, idempotency_key)
			WHERE idempotency_key IS NOT NULL
			DO NOTHING
		RETURNING `+jobColumns,
		params.JobID, params.WorkspaceID, assetID, params.CreatedBy, params.Kind,
		params.Payload, params.MaxAttempts, params.IdempotencyKey, params.RequestHash,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, hash, loadErr := loadByIdempotencyKey(ctx, tx, params.WorkspaceID, params.IdempotencyKey)
		if loadErr != nil {
			return Job{}, false, fmt.Errorf("load concurrently created correction job: %w", loadErr)
		}
		if hash != params.RequestHash {
			return Job{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}
	if isUniqueViolation(err) {
		return Job{}, false, ErrCorrectionActive
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("insert correction job: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES (
			$1, $2, $3, 'user', 'correction.requested', 'job', $4,
			jsonb_build_object('source_revision_id', $5::text)
		)`, params.AuditID, params.WorkspaceID, params.CreatedBy, params.JobID, params.SourceRevisionID,
	); err != nil {
		return Job{}, false, fmt.Errorf("insert correction audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, false, fmt.Errorf("commit correction job: %w", err)
	}
	return created, false, nil
}

func correctableRevisionKind(kind string) bool {
	switch kind {
	case "raw_asr", "normalized", "llm_corrected", "human_edited", "approved":
		return true
	default:
		return false
	}
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

const jobColumns = `
	id::text, workspace_id::text, COALESCE(asset_id::text, ''), created_by::text,
	kind, state, payload, attempts, max_attempts, available_at,
	lease_owner, lease_expires_at, last_error_code, result_revision_id::text,
	created_at, updated_at`

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreateTranscription(
	ctx context.Context,
	params CreateTranscriptionParams,
) (Job, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Job{}, false, fmt.Errorf("begin transcription job transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if existing, requestHash, err := loadByIdempotencyKey(
		ctx, tx, params.WorkspaceID, params.IdempotencyKey,
	); err == nil {
		if requestHash != params.RequestHash {
			return Job{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, fmt.Errorf("load idempotent transcription job: %w", err)
	}

	var assetState string
	var hasOriginal bool
	err = tx.QueryRow(ctx, `
		SELECT a.status,
		       EXISTS (
		           SELECT 1 FROM asset_objects object
		           WHERE object.asset_id = a.id AND object.kind = 'original'
		       )
		FROM assets a
		WHERE a.id = $1 AND a.workspace_id = $2 AND a.deleted_at IS NULL
		FOR UPDATE OF a`, params.AssetID, params.WorkspaceID).Scan(&assetState, &hasOriginal)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, ErrNotFound
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("lock transcription asset: %w", err)
	}

	// A same-key request may have committed while this transaction waited for
	// the asset row lock. Re-check before interpreting processing as a conflict.
	if existing, requestHash, err := loadByIdempotencyKey(
		ctx, tx, params.WorkspaceID, params.IdempotencyKey,
	); err == nil {
		if requestHash != params.RequestHash {
			return Job{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, fmt.Errorf("reload idempotent transcription job: %w", err)
	}
	if assetState != "ready" || !hasOriginal {
		return Job{}, false, ErrAssetNotReady
	}

	created, err := scanJob(tx.QueryRow(ctx, `
		INSERT INTO jobs (
			id, workspace_id, asset_id, created_by, kind, state, payload,
			max_attempts, idempotency_key, idempotency_request_hash
		) VALUES ($1, $2, $3, $4, $5, 'queued', $6, $7, $8, $9)
		ON CONFLICT (workspace_id, idempotency_key)
			WHERE idempotency_key IS NOT NULL
			DO NOTHING
		RETURNING `+jobColumns,
		params.JobID, params.WorkspaceID, params.AssetID, params.CreatedBy,
		params.Kind, params.Payload, params.MaxAttempts,
		params.IdempotencyKey, params.RequestHash,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, requestHash, loadErr := loadByIdempotencyKey(
			ctx, tx, params.WorkspaceID, params.IdempotencyKey,
		)
		if loadErr != nil {
			return Job{}, false, fmt.Errorf("load concurrently created transcription job: %w", loadErr)
		}
		if requestHash != params.RequestHash {
			return Job{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("insert transcription job: %w", err)
	}
	commandTag, err := tx.Exec(ctx, `
		UPDATE assets
		SET status = 'processing', version = version + 1, updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2 AND status = 'ready'`,
		params.AssetID, params.WorkspaceID,
	)
	if err != nil {
		return Job{}, false, fmt.Errorf("mark transcription asset processing: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return Job{}, false, ErrAssetNotReady
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES (
			$1, $2, $3, 'user', 'transcription.requested', 'job', $4,
			jsonb_build_object('asset_id', $5::text)
		)`,
		params.AuditID, params.WorkspaceID, params.CreatedBy, params.JobID,
		params.AssetID,
	); err != nil {
		return Job{}, false, fmt.Errorf("insert transcription audit log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, false, fmt.Errorf("commit transcription job transaction: %w", err)
	}
	return created, false, nil
}

func (r *PostgresRepository) Get(ctx context.Context, workspaceID, jobID string) (Job, error) {
	result, err := scanJob(r.pool.QueryRow(ctx, `
		SELECT `+jobColumns+`
		FROM jobs
		WHERE id = $1 AND workspace_id = $2`, jobID, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("query job: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) Claim(ctx context.Context, params ClaimParams) (Job, error) {
	params.WorkerID = strings.TrimSpace(params.WorkerID)
	if params.Kind == "" || !validWorkerID(params.WorkerID) || params.Now.IsZero() || params.LeaseDuration <= 0 {
		return Job{}, ErrInvalidInput
	}
	now := params.Now.UTC()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Job{}, fmt.Errorf("begin claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tx.QueryRow(ctx, `
		SELECT GREATEST($1::timestamptz, clock_timestamp())`, now,
	).Scan(&now); err != nil {
		return Job{}, fmt.Errorf("resolve claim clock: %w", err)
	}

	cleanedExpired := false
	for {
		candidate, err := scanJob(tx.QueryRow(ctx, `
			SELECT `+jobColumns+`
			FROM jobs
			WHERE kind = $1
			  AND (
			      (state IN ('queued', 'retry_wait') AND available_at <= $2 AND attempts < max_attempts)
			      OR (state = 'running' AND lease_expires_at <= $2)
			  )
			ORDER BY
			  CASE WHEN state = 'running' THEN 0 ELSE 1 END,
			  available_at,
			  created_at,
			  id
			FOR UPDATE SKIP LOCKED
			LIMIT 1`, params.Kind, now))
		if errors.Is(err, pgx.ErrNoRows) {
			if cleanedExpired {
				if commitErr := tx.Commit(ctx); commitErr != nil {
					return Job{}, fmt.Errorf("commit expired lease cleanup: %w", commitErr)
				}
			}
			return Job{}, ErrNoClaimableJob
		}
		if err != nil {
			return Job{}, fmt.Errorf("select claimable job: %w", err)
		}

		if candidate.State == StateRunning {
			if err := finishAttempt(
				ctx, tx, candidate.ID, candidate.Attempts, "lease_expired", ErrorCodeLeaseExpired, now,
			); err != nil {
				return Job{}, err
			}
			if candidate.Attempts >= candidate.MaxAttempts {
				if _, err := tx.Exec(ctx, `
					UPDATE jobs
					SET state = 'failed', lease_owner = NULL, lease_expires_at = NULL,
					    last_error_code = $2, updated_at = $3
					WHERE id = $1`, candidate.ID, ErrorCodeLeaseExpired, now); err != nil {
					return Job{}, fmt.Errorf("fail exhausted expired job: %w", err)
				}
				if candidate.AssetID != "" {
					if _, err := tx.Exec(ctx, `
						UPDATE assets
						SET status = 'failed', version = version + 1, updated_at = $3
						WHERE id = $1 AND workspace_id = $2 AND status = 'processing'`,
						candidate.AssetID, candidate.WorkspaceID, now,
					); err != nil {
						return Job{}, fmt.Errorf("fail asset after expired job: %w", err)
					}
				}
				cleanedExpired = true
				continue
			}
		}

		nextAttempt := candidate.Attempts + 1
		claimed, err := scanJob(tx.QueryRow(ctx, `
			UPDATE jobs
			SET state = 'running', attempts = $2, lease_owner = $3,
			    lease_expires_at = $4, last_error_code = NULL, updated_at = $5
			WHERE id = $1
			RETURNING `+jobColumns,
			candidate.ID, nextAttempt, params.WorkerID,
			now.Add(params.LeaseDuration), now,
		))
		if err != nil {
			return Job{}, fmt.Errorf("lease claimed job: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO job_attempts (job_id, attempt, worker_id, started_at)
			VALUES ($1, $2, $3, $4)`,
			candidate.ID, nextAttempt, params.WorkerID, now,
		); err != nil {
			return Job{}, fmt.Errorf("record job attempt: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return Job{}, fmt.Errorf("commit job claim: %w", err)
		}
		return claimed, nil
	}
}

func (r *PostgresRepository) Succeed(ctx context.Context, params SucceedParams) (Job, error) {
	params.WorkerID = strings.TrimSpace(params.WorkerID)
	if params.JobID == "" || !validWorkerID(params.WorkerID) || params.Now.IsZero() {
		return Job{}, ErrInvalidInput
	}
	now := params.Now.UTC()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Job{}, fmt.Errorf("begin job success transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tx.QueryRow(ctx, `
		SELECT GREATEST($1::timestamptz, clock_timestamp())`, now,
	).Scan(&now); err != nil {
		return Job{}, fmt.Errorf("resolve success clock: %w", err)
	}
	candidate, err := lockJob(ctx, tx, params.JobID)
	if err != nil {
		return Job{}, err
	}
	if !ownsLiveLease(candidate, params.WorkerID, now) {
		return Job{}, ErrLeaseConflict
	}
	if err := finishAttempt(ctx, tx, candidate.ID, candidate.Attempts, "succeeded", "", now); err != nil {
		return Job{}, err
	}
	completed, err := scanJob(tx.QueryRow(ctx, `
		UPDATE jobs
		SET state = 'succeeded', lease_owner = NULL, lease_expires_at = NULL,
		    last_error_code = NULL, result_revision_id = $2, updated_at = $3
		WHERE id = $1
		RETURNING `+jobColumns, candidate.ID, params.ResultRevisionID, now))
	if err != nil {
		return Job{}, fmt.Errorf("complete succeeded job: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, fmt.Errorf("commit job success: %w", err)
	}
	return completed, nil
}

func (r *PostgresRepository) Fail(ctx context.Context, params FailParams) (Job, error) {
	params.WorkerID = strings.TrimSpace(params.WorkerID)
	if !IsSafeErrorCode(params.ErrorCode) {
		return Job{}, ErrInvalidErrorCode
	}
	if params.JobID == "" || !validWorkerID(params.WorkerID) || params.Now.IsZero() ||
		params.RetryAt.IsZero() || !params.RetryAt.After(params.Now) {
		return Job{}, ErrInvalidInput
	}
	retryDelay := params.RetryAt.Sub(params.Now)
	now := params.Now.UTC()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Job{}, fmt.Errorf("begin job failure transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tx.QueryRow(ctx, `
		SELECT GREATEST($1::timestamptz, clock_timestamp())`, now,
	).Scan(&now); err != nil {
		return Job{}, fmt.Errorf("resolve failure clock: %w", err)
	}
	candidate, err := lockJob(ctx, tx, params.JobID)
	if err != nil {
		return Job{}, err
	}
	if !ownsLiveLease(candidate, params.WorkerID, now) {
		return Job{}, ErrLeaseConflict
	}

	nextState := StateRetryWait
	outcome := "retry"
	availableAt := now.Add(retryDelay)
	if candidate.Attempts >= candidate.MaxAttempts {
		nextState = StateFailed
		outcome = "failed"
		availableAt = candidate.AvailableAt
	}
	if err := finishAttempt(
		ctx, tx, candidate.ID, candidate.Attempts, outcome, params.ErrorCode, now,
	); err != nil {
		return Job{}, err
	}
	failed, err := scanJob(tx.QueryRow(ctx, `
		UPDATE jobs
		SET state = $2, available_at = $3, lease_owner = NULL, lease_expires_at = NULL,
		    last_error_code = $4, updated_at = $5
		WHERE id = $1
		RETURNING `+jobColumns,
		candidate.ID, nextState, availableAt, params.ErrorCode, now,
	))
	if err != nil {
		return Job{}, fmt.Errorf("complete failed job attempt: %w", err)
	}
	if nextState == StateFailed && candidate.AssetID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE assets
			SET status = 'failed', version = version + 1, updated_at = $3
			WHERE id = $1 AND workspace_id = $2 AND status = 'processing'`,
			candidate.AssetID, candidate.WorkspaceID, now,
		); err != nil {
			return Job{}, fmt.Errorf("fail asset after terminal job: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, fmt.Errorf("commit job failure: %w", err)
	}
	return failed, nil
}

func loadByIdempotencyKey(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, key string,
) (Job, string, error) {
	var requestHash string
	result, err := scanJobWithHash(tx.QueryRow(ctx, `
		SELECT `+jobColumns+`, idempotency_request_hash
		FROM jobs
		WHERE workspace_id = $1 AND idempotency_key = $2`, workspaceID, key), &requestHash)
	return result, strings.TrimSpace(requestHash), err
}

func lockJob(ctx context.Context, tx pgx.Tx, jobID string) (Job, error) {
	result, err := scanJob(tx.QueryRow(ctx, `
		SELECT `+jobColumns+`
		FROM jobs
		WHERE id = $1
		FOR UPDATE`, jobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("lock job: %w", err)
	}
	return result, nil
}

func finishAttempt(
	ctx context.Context,
	tx pgx.Tx,
	jobID string,
	attempt int,
	outcome, errorCode string,
	finishedAt time.Time,
) error {
	var nullableErrorCode any
	if errorCode != "" {
		nullableErrorCode = errorCode
	}
	commandTag, err := tx.Exec(ctx, `
		UPDATE job_attempts
		SET outcome = $3, error_code = $4, finished_at = $5
		WHERE job_id = $1 AND attempt = $2 AND outcome IS NULL`,
		jobID, attempt, outcome, nullableErrorCode, finishedAt,
	)
	if err != nil {
		return fmt.Errorf("finish job attempt: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return fmt.Errorf("finish job attempt: %w", ErrLeaseConflict)
	}
	return nil
}

func ownsLiveLease(candidate Job, workerID string, now time.Time) bool {
	return candidate.State == StateRunning &&
		candidate.LeaseOwner != nil && *candidate.LeaseOwner == workerID &&
		candidate.LeaseExpiresAt != nil && candidate.LeaseExpiresAt.After(now)
}

func validWorkerID(workerID string) bool {
	if len(workerID) < 1 || len(workerID) > 200 {
		return false
	}
	for _, character := range workerID {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var result Job
	var payload []byte
	err := row.Scan(
		&result.ID,
		&result.WorkspaceID,
		&result.AssetID,
		&result.CreatedBy,
		&result.Kind,
		&result.State,
		&payload,
		&result.Attempts,
		&result.MaxAttempts,
		&result.AvailableAt,
		&result.LeaseOwner,
		&result.LeaseExpiresAt,
		&result.LastErrorCode,
		&result.ResultRevisionID,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	result.Payload = append(result.Payload[:0], payload...)
	return result, err
}

func scanJobWithHash(row rowScanner, requestHash *string) (Job, error) {
	var result Job
	var payload []byte
	err := row.Scan(
		&result.ID,
		&result.WorkspaceID,
		&result.AssetID,
		&result.CreatedBy,
		&result.Kind,
		&result.State,
		&payload,
		&result.Attempts,
		&result.MaxAttempts,
		&result.AvailableAt,
		&result.LeaseOwner,
		&result.LeaseExpiresAt,
		&result.LastErrorCode,
		&result.ResultRevisionID,
		&result.CreatedAt,
		&result.UpdatedAt,
		requestHash,
	)
	result.Payload = append(result.Payload[:0], payload...)
	return result, err
}
