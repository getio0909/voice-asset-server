package clip

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Create(ctx context.Context, params CreateParams) (Clip, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Clip{}, fmt.Errorf("begin clip creation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var sourceExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM asset_objects AS object
			JOIN assets AS asset ON asset.id = object.asset_id
			WHERE object.id = $1 AND object.asset_id = $2
			  AND object.kind = 'original' AND asset.workspace_id = $3
			  AND asset.deleted_at IS NULL
		)`, params.ParentObjectID, params.AssetID, params.WorkspaceID,
	).Scan(&sourceExists); err != nil {
		return Clip{}, fmt.Errorf("check clip source: %w", err)
	}
	if !sourceExists {
		return Clip{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, parent_object_id, kind, storage_backend, storage_key,
			mime_type, container, codec, sample_rate, channel_count, bitrate,
			duration_ms, file_size, sha256, creation_source, encryption_state
		) VALUES (
			$1, $2, $3, 'clip', $4, $5,
			'audio/wav', 'wav', 'pcm_s16le', $6, $7, $8,
			$9, $10, $11, 'agent_clip', 'none'
		)`, params.ID, params.AssetID, params.ParentObjectID, params.StorageBackend, params.StorageKey,
		params.SampleRate, params.ChannelCount, params.Bitrate, params.DurationMS,
		params.FileSize, params.SHA256,
	); err != nil {
		return Clip{}, fmt.Errorf("insert clip object: %w", err)
	}
	var created Clip
	if err := tx.QueryRow(ctx, `
		INSERT INTO audio_clips (
			id, workspace_id, asset_id, start_ms, end_ms, created_by, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, asset_id::text, start_ms, end_ms, created_at, expires_at`,
		params.ID, params.WorkspaceID, params.AssetID, params.StartMS, params.EndMS,
		params.ActorID, params.ExpiresAt,
	).Scan(&created.ID, &created.AssetID, &created.StartMS, &created.EndMS, &created.CreatedAt, &created.ExpiresAt); err != nil {
		return Clip{}, fmt.Errorf("insert clip record: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, $4, 'audio_clip.created', 'audio_clip', $5, $6,
			jsonb_build_object(
				'asset_id', $7::uuid, 'start_ms', $8::bigint, 'end_ms', $9::bigint,
				'api_key_id', NULLIF($10::text, '')
			)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.ActorType,
		params.ID, params.RequestID, params.AssetID, params.StartMS, params.EndMS,
		params.CredentialID,
	); err != nil {
		return Clip{}, fmt.Errorf("insert clip audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Clip{}, fmt.Errorf("commit clip creation: %w", err)
	}
	created.DurationMS = params.DurationMS
	created.MIMEType = "audio/wav"
	created.FileSize = params.FileSize
	created.SHA256 = params.SHA256
	return created, nil
}

func (repository *PostgresRepository) Get(
	ctx context.Context,
	workspaceID,
	clipID string,
	now time.Time,
) (StoredClip, error) {
	var result StoredClip
	err := repository.pool.QueryRow(ctx, `
		SELECT clip.id::text, clip.asset_id::text, clip.start_ms, clip.end_ms,
		       object.duration_ms, object.mime_type, object.file_size, object.sha256,
		       object.storage_backend, object.storage_key, clip.created_at, clip.expires_at
		FROM audio_clips AS clip
		JOIN asset_objects AS object ON object.id = clip.id
		JOIN assets AS asset ON asset.id = clip.asset_id
		WHERE clip.workspace_id = $1 AND clip.id = $2 AND clip.expires_at > $3
		  AND asset.deleted_at IS NULL`, workspaceID, clipID, now,
	).Scan(
		&result.ID, &result.AssetID, &result.StartMS, &result.EndMS,
		&result.DurationMS, &result.MIMEType, &result.FileSize, &result.SHA256,
		&result.StorageBackend, &result.StorageKey, &result.CreatedAt, &result.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredClip{}, ErrNotFound
	}
	if err != nil {
		return StoredClip{}, fmt.Errorf("query audio clip: %w", err)
	}
	result.DownloadURL = "/api/v1/audio-clips/" + result.ID
	return result, nil
}
