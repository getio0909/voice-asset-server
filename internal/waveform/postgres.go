package waveform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresOriginalRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresOriginalRepository(pool *pgxpool.Pool) *PostgresOriginalRepository {
	return &PostgresOriginalRepository{pool: pool}
}

// GetOriginal intentionally includes trashed assets so migration backfill can
// complete before a later restore. Public reads remain hidden while trashed.
func (repository *PostgresOriginalRepository) GetOriginal(
	ctx context.Context,
	workspaceID, assetID string,
) (audio.Original, error) {
	var result audio.Original
	var container *string
	var sampleRate *int
	err := repository.pool.QueryRow(ctx, `
		SELECT object.id::text, object.asset_id::text, object.storage_key, object.mime_type,
		       object.container, object.sample_rate, object.file_size, object.sha256,
		       COALESCE(object.duration_ms, asset.duration_ms, 0)
		FROM asset_objects object
		JOIN assets asset ON asset.id = object.asset_id
		WHERE object.asset_id = $1 AND asset.workspace_id = $2
		  AND object.kind = 'original'`, assetID, workspaceID,
	).Scan(
		&result.ObjectID, &result.AssetID, &result.StorageKey, &result.MIMEType,
		&container, &sampleRate, &result.Size, &result.SHA256, &result.DurationMS,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return audio.Original{}, audio.ErrAudioNotFound
	}
	if err != nil {
		return audio.Original{}, fmt.Errorf("query waveform source: %w", err)
	}
	if container != nil {
		result.Container = *container
	}
	if sampleRate != nil {
		result.SampleRate = *sampleRate
	}
	return result, nil
}

type PostgresCommitter struct {
	pool *pgxpool.Pool
}

func NewPostgresCommitter(pool *pgxpool.Pool) *PostgresCommitter {
	return &PostgresCommitter{pool: pool}
}

func (committer *PostgresCommitter) Commit(ctx context.Context, params CommitParams) error {
	digest, hashErr := hex.DecodeString(params.Object.SHA256)
	if params.JobID == "" || params.WorkerID == "" || params.WorkspaceID == "" ||
		params.AssetID == "" || params.ActorID == "" || params.AuditID == "" ||
		params.Object.Key == "" || params.Object.Size <= 0 || hashErr != nil || len(digest) != sha256.Size ||
		params.Width != Width || params.Height != Height || params.Now.IsZero() {
		return ErrProcessingFailed
	}
	tx, err := committer.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin waveform commit: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tx.QueryRow(ctx, `SELECT GREATEST($1::timestamptz, clock_timestamp())`, params.Now).Scan(&params.Now); err != nil {
		return fmt.Errorf("resolve waveform commit clock: %w", err)
	}
	var state, kind, workspaceID, assetID, createdBy string
	var leaseOwner *string
	var leaseExpiresAt *time.Time
	var attempt int
	err = tx.QueryRow(ctx, `
		SELECT state, kind, workspace_id::text, asset_id::text, created_by::text,
		       lease_owner, lease_expires_at, attempts
		FROM jobs WHERE id = $1 FOR UPDATE`, params.JobID,
	).Scan(&state, &kind, &workspaceID, &assetID, &createdBy, &leaseOwner, &leaseExpiresAt, &attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return job.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock waveform job: %w", err)
	}
	if state != job.StateRunning || kind != job.KindGenerateWaveform ||
		workspaceID != params.WorkspaceID || assetID != params.AssetID || createdBy != params.ActorID ||
		leaseOwner == nil || *leaseOwner != params.WorkerID ||
		leaseExpiresAt == nil || !leaseExpiresAt.After(params.Now) {
		return job.ErrLeaseConflict
	}
	var parentObjectID string
	var durationMS int64
	err = tx.QueryRow(ctx, `
		SELECT original.id::text, COALESCE(original.duration_ms, asset.duration_ms, 0)
		FROM asset_objects original
		JOIN assets asset ON asset.id = original.asset_id
		WHERE original.asset_id = $1 AND asset.workspace_id = $2
		  AND original.kind = 'original'
		FOR UPDATE OF original, asset`, params.AssetID, params.WorkspaceID,
	).Scan(&parentObjectID, &durationMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return job.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock waveform source: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, parent_object_id, kind, storage_backend, storage_key,
			mime_type, container, codec, duration_ms, file_size, sha256,
			creation_source, encryption_state, created_at
		) VALUES (
			$1, $2, $3, 'waveform', $4, $5,
			'image/png', 'png', 'png', $6, $7, $8,
			'worker_waveform', 'none', $9
		)`, params.JobID, params.AssetID, parentObjectID, params.Object.Backend, params.Object.Key,
		durationMS, params.Object.Size, params.Object.SHA256, params.Now); err != nil {
		return fmt.Errorf("insert waveform object: %w", err)
	}
	commandTag, err := tx.Exec(ctx, `
		UPDATE job_attempts
		SET outcome = 'succeeded', error_code = NULL, finished_at = $3
		WHERE job_id = $1 AND attempt = $2 AND outcome IS NULL`, params.JobID, attempt, params.Now)
	if err != nil {
		return fmt.Errorf("finish waveform attempt: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return job.ErrLeaseConflict
	}
	commandTag, err = tx.Exec(ctx, `
		UPDATE jobs
		SET state = 'succeeded', lease_owner = NULL, lease_expires_at = NULL,
		    last_error_code = NULL, updated_at = $2
		WHERE id = $1 AND state = 'running'`, params.JobID, params.Now)
	if err != nil {
		return fmt.Errorf("succeed waveform job: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return job.ErrLeaseConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action,
			target_type, target_id, metadata, occurred_at
		) VALUES (
			$1, $2, NULL, 'system', 'waveform.generated',
			'asset_waveform', $3,
			jsonb_build_object(
				'asset_id', $4::uuid::text, 'job_id', $3::uuid::text,
				'requested_by', $5::uuid::text, 'width', $6::integer, 'height', $7::integer
			), $8
		)`, params.AuditID, params.WorkspaceID, params.JobID, params.AssetID,
		params.ActorID, params.Width, params.Height, params.Now); err != nil {
		return fmt.Errorf("insert waveform audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit waveform: %w", err)
	}
	return nil
}
