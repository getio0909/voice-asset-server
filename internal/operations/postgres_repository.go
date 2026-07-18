package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) ListJobs(
	ctx context.Context,
	params JobListParams,
) ([]JobSummary, error) {
	var beforeAt, beforeID any
	if params.BeforeUpdatedAt != nil {
		beforeAt = *params.BeforeUpdatedAt
		beforeID = params.BeforeID
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT candidate.id::text, COALESCE(candidate.asset_id::text, ''), candidate.created_by::text,
		       candidate.kind, candidate.state, candidate.attempts, candidate.max_attempts,
		       candidate.available_at, candidate.lease_expires_at, candidate.last_error_code,
		       candidate.result_revision_id::text, candidate.created_at, candidate.updated_at,
		       candidate.state = 'failed' AND candidate.result_revision_id IS NULL
		         AND candidate.max_attempts < 20
		         AND CASE candidate.kind
		           WHEN 'mock_transcribe' THEN asset.status = 'failed' AND asset.deleted_at IS NULL
		           WHEN 'llm_correct' THEN asset.status = 'ready' AND asset.deleted_at IS NULL
		           WHEN 'generate_waveform' THEN asset.status = 'ready' AND asset.deleted_at IS NULL
		           WHEN 'purge_asset' THEN asset.status = 'purging'
		           ELSE false
		         END
		FROM jobs candidate
		LEFT JOIN assets asset
		  ON asset.id = candidate.asset_id AND asset.workspace_id = candidate.workspace_id
		WHERE candidate.workspace_id = $1
		  AND ($2 = '' OR candidate.state = $2)
		  AND ($3 = '' OR candidate.kind = $3)
		  AND ($4::timestamptz IS NULL OR (candidate.updated_at, candidate.id) < ($4::timestamptz, $5::uuid))
		ORDER BY candidate.updated_at DESC, candidate.id DESC
		LIMIT $6`, params.WorkspaceID, params.State, params.Kind, beforeAt, beforeID, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("query operations jobs: %w", err)
	}
	defer rows.Close()
	items := make([]JobSummary, 0)
	for rows.Next() {
		var item JobSummary
		if err := rows.Scan(
			&item.ID, &item.AssetID, &item.CreatedBy, &item.Kind, &item.State,
			&item.Attempts, &item.MaxAttempts, &item.AvailableAt,
			&item.LeaseExpiresAt, &item.LastErrorCode, &item.ResultRevisionID,
			&item.CreatedAt, &item.UpdatedAt, &item.Retryable,
		); err != nil {
			return nil, fmt.Errorf("scan operations job: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operations jobs: %w", err)
	}
	return items, nil
}

func (repository *PostgresRepository) RetryJob(
	ctx context.Context,
	params RetryJobParams,
) (JobSummary, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return JobSummary{}, fmt.Errorf("begin operations job retry: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	availableAt := params.AvailableAt.UTC()
	if err := tx.QueryRow(ctx, `
		SELECT GREATEST($1::timestamptz, clock_timestamp())`, availableAt,
	).Scan(&availableAt); err != nil {
		return JobSummary{}, fmt.Errorf("resolve operations retry clock: %w", err)
	}
	var current JobSummary
	err = tx.QueryRow(ctx, `
		SELECT id::text, COALESCE(asset_id::text, ''), created_by::text,
		       kind, state, attempts, max_attempts, available_at,
		       lease_expires_at, last_error_code, result_revision_id::text,
		       created_at, updated_at, false
		FROM jobs
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, params.JobID, params.WorkspaceID).Scan(
		&current.ID, &current.AssetID, &current.CreatedBy, &current.Kind, &current.State,
		&current.Attempts, &current.MaxAttempts, &current.AvailableAt,
		&current.LeaseExpiresAt, &current.LastErrorCode, &current.ResultRevisionID,
		&current.CreatedAt, &current.UpdatedAt, &current.Retryable,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobSummary{}, ErrNotFound
	}
	if err != nil {
		return JobSummary{}, fmt.Errorf("lock operations job retry: %w", err)
	}
	if current.State != job.StateFailed || current.ResultRevisionID != nil || current.AssetID == "" {
		return JobSummary{}, ErrNotRetryable
	}
	if current.MaxAttempts >= 20 {
		return JobSummary{}, ErrRetryLimit
	}
	var assetState string
	var assetDeleted bool
	if err := tx.QueryRow(ctx, `
		SELECT status, deleted_at IS NOT NULL
		FROM assets
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, current.AssetID, params.WorkspaceID).Scan(&assetState, &assetDeleted); errors.Is(err, pgx.ErrNoRows) {
		return JobSummary{}, ErrNotRetryable
	} else if err != nil {
		return JobSummary{}, fmt.Errorf("lock operations retry asset: %w", err)
	}
	eligible := false
	switch current.Kind {
	case job.KindMockTranscribe:
		eligible = assetState == "failed" && !assetDeleted
	case job.KindLLMCorrect, job.KindGenerateWaveform:
		eligible = assetState == "ready" && !assetDeleted
	case job.KindPurgeAsset:
		eligible = assetState == "purging"
	}
	if !eligible {
		return JobSummary{}, ErrNotRetryable
	}
	if current.Kind == job.KindMockTranscribe {
		command, updateErr := tx.Exec(ctx, `
			UPDATE assets
			SET status = 'processing', version = version + 1, updated_at = $3
			WHERE id = $1 AND workspace_id = $2 AND status = 'failed' AND deleted_at IS NULL`,
			current.AssetID, params.WorkspaceID, availableAt,
		)
		if updateErr != nil {
			return JobSummary{}, fmt.Errorf("restore retried transcription asset: %w", updateErr)
		}
		if command.RowsAffected() != 1 {
			return JobSummary{}, ErrNotRetryable
		}
	}

	retried := JobSummary{}
	err = tx.QueryRow(ctx, `
		UPDATE jobs
		SET state = 'queued', max_attempts = max_attempts + 1,
		    available_at = $3, lease_owner = NULL, lease_expires_at = NULL,
		    last_error_code = NULL, updated_at = $3
		WHERE id = $1 AND workspace_id = $2 AND state = 'failed'
		RETURNING id::text, COALESCE(asset_id::text, ''), created_by::text,
		          kind, state, attempts, max_attempts, available_at,
		          lease_expires_at, last_error_code, result_revision_id::text,
		          created_at, updated_at, false`,
		params.JobID, params.WorkspaceID, availableAt,
	).Scan(
		&retried.ID, &retried.AssetID, &retried.CreatedBy, &retried.Kind, &retried.State,
		&retried.Attempts, &retried.MaxAttempts, &retried.AvailableAt,
		&retried.LeaseExpiresAt, &retried.LastErrorCode, &retried.ResultRevisionID,
		&retried.CreatedAt, &retried.UpdatedAt, &retried.Retryable,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return JobSummary{}, ErrNotRetryable
	}
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return JobSummary{}, ErrNotRetryable
		}
		return JobSummary{}, fmt.Errorf("queue operations job retry: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata, occurred_at
		) VALUES (
			$1, $2, $3, 'user', 'admin.job.retried', 'job', $4, $5,
			jsonb_strip_nulls(jsonb_build_object(
				'kind', $6::text, 'asset_id', $7::uuid::text,
				'previous_state', 'failed', 'previous_attempts', $8::integer,
				'previous_max_attempts', $9::integer,
				'new_max_attempts', $10::integer, 'last_error_code', $11::text
			)), $12
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.JobID,
		params.RequestID, current.Kind, current.AssetID, current.Attempts,
		current.MaxAttempts, retried.MaxAttempts, current.LastErrorCode, availableAt,
	); err != nil {
		return JobSummary{}, fmt.Errorf("insert operations job retry audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return JobSummary{}, fmt.Errorf("commit operations job retry: %w", err)
	}
	return retried, nil
}

func (repository *PostgresRepository) ListAuditLogs(
	ctx context.Context,
	params AuditListParams,
) ([]AuditEntry, error) {
	var beforeAt, beforeID any
	if params.BeforeOccurredAt != nil {
		beforeAt = *params.BeforeOccurredAt
		beforeID = params.BeforeID
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT audit.id::text, COALESCE(audit.actor_id::text, ''),
		       COALESCE(actor.email, ''), audit.actor_type, audit.action,
		       audit.target_type, COALESCE(audit.target_id::text, ''),
		       COALESCE(audit.request_id, ''), audit.metadata, audit.occurred_at
		FROM audit_logs audit
		LEFT JOIN users actor ON actor.id = audit.actor_id
		WHERE audit.workspace_id = $1
		  AND ($2 = '' OR audit.actor_type = $2)
		  AND ($3 = '' OR audit.action = $3)
		  AND ($4 = '' OR audit.target_type = $4)
		  AND ($5::timestamptz IS NULL OR (audit.occurred_at, audit.id) < ($5::timestamptz, $6::uuid))
		ORDER BY audit.occurred_at DESC, audit.id DESC
		LIMIT $7`, params.WorkspaceID, params.ActorType, params.Action, params.TargetType,
		beforeAt, beforeID, params.Limit)
	if err != nil {
		return nil, fmt.Errorf("query operations audit logs: %w", err)
	}
	defer rows.Close()
	items := make([]AuditEntry, 0)
	for rows.Next() {
		var item AuditEntry
		var metadata []byte
		if err := rows.Scan(
			&item.ID, &item.ActorID, &item.ActorEmail, &item.ActorType, &item.Action,
			&item.TargetType, &item.TargetID, &item.RequestID, &metadata, &item.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("scan operations audit log: %w", err)
		}
		item.Metadata = json.RawMessage(metadata)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operations audit logs: %w", err)
	}
	return items, nil
}

func (repository *PostgresRepository) GetSystemStatus(
	ctx context.Context,
	workspaceID string,
) (SystemStatus, error) {
	var result SystemStatus
	err := repository.pool.QueryRow(ctx, `
		SELECT clock_timestamp(),
		       (SELECT count(*) FROM memberships membership
		        JOIN users account ON account.id = membership.user_id
		        WHERE membership.workspace_id = $1 AND account.status = 'active'),
		       (SELECT count(*) FROM assets WHERE workspace_id = $1),
		       (SELECT count(*) FROM assets WHERE workspace_id = $1 AND deleted_at IS NULL),
		       (SELECT count(*) FROM assets WHERE workspace_id = $1 AND status = 'trashed'),
		       (SELECT count(*) FROM assets WHERE workspace_id = $1 AND status = 'purging'),
		       (SELECT count(*) FROM assets WHERE workspace_id = $1 AND status = 'failed'),
		       (SELECT COALESCE(sum(duration_ms), 0) FROM assets
		        WHERE workspace_id = $1 AND deleted_at IS NULL),
		       (SELECT count(*) FROM asset_objects object
		        JOIN assets asset ON asset.id = object.asset_id WHERE asset.workspace_id = $1),
		       (SELECT COALESCE(sum(object.file_size), 0) FROM asset_objects object
		        JOIN assets asset ON asset.id = object.asset_id WHERE asset.workspace_id = $1),
		       (SELECT count(*) FROM transcripts transcript
		        JOIN assets asset ON asset.id = transcript.asset_id WHERE asset.workspace_id = $1),
		       (SELECT count(*) FROM transcript_revisions revision
		        JOIN transcripts transcript ON transcript.id = revision.transcript_id
		        JOIN assets asset ON asset.id = transcript.asset_id WHERE asset.workspace_id = $1),
		       (SELECT count(*) FROM jobs WHERE workspace_id = $1),
		       (SELECT count(*) FROM jobs WHERE workspace_id = $1 AND state = 'queued'),
		       (SELECT count(*) FROM jobs WHERE workspace_id = $1 AND state = 'running'),
		       (SELECT count(*) FROM jobs WHERE workspace_id = $1 AND state = 'retry_wait'),
		       (SELECT count(*) FROM jobs WHERE workspace_id = $1 AND state = 'succeeded'),
		       (SELECT count(*) FROM jobs WHERE workspace_id = $1 AND state = 'failed'),
		       (SELECT count(*) FROM jobs WHERE workspace_id = $1 AND state = 'cancelled'),
		       (SELECT count(*) FROM provider_profiles
		        WHERE workspace_id = $1 AND provider_type = 'asr' AND state = 'enabled'),
		       (SELECT count(*) FROM provider_profiles
		        WHERE workspace_id = $1 AND provider_type = 'llm' AND state = 'enabled')`, workspaceID).Scan(
		&result.GeneratedAt, &result.ActiveUsers,
		&result.Assets.Total, &result.Assets.Active, &result.Assets.Trashed,
		&result.Assets.Purging, &result.Assets.Failed, &result.Assets.AudioDurationMS,
		&result.Storage.ObjectCount, &result.Storage.Bytes,
		&result.Transcripts.TranscriptCount, &result.Transcripts.RevisionCount,
		&result.Jobs.Total, &result.Jobs.Queued, &result.Jobs.Running,
		&result.Jobs.RetryWait, &result.Jobs.Succeeded, &result.Jobs.Failed,
		&result.Jobs.Cancelled, &result.Providers.EnabledASR, &result.Providers.EnabledLLM,
	)
	if err != nil {
		return SystemStatus{}, fmt.Errorf("query operations system status: %w", err)
	}
	return result, nil
}
