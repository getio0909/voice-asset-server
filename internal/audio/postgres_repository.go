package audio

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresOriginalRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresOriginalRepository(pool *pgxpool.Pool) *PostgresOriginalRepository {
	return &PostgresOriginalRepository{pool: pool}
}

func (r *PostgresOriginalRepository) GetOriginal(
	ctx context.Context,
	workspaceID,
	assetID string,
) (Original, error) {
	var result Original
	var container *string
	var sampleRate *int
	err := r.pool.QueryRow(ctx, `
		SELECT object.id::text, object.asset_id::text, object.storage_backend, object.storage_key, object.mime_type,
		       object.container, object.sample_rate, object.file_size, object.sha256,
		       COALESCE(object.duration_ms, asset.duration_ms, 0)
		FROM asset_objects object
		JOIN assets asset ON asset.id = object.asset_id
		WHERE object.asset_id = $1 AND asset.workspace_id = $2
		  AND object.kind = 'original' AND asset.deleted_at IS NULL`,
		assetID, workspaceID,
	).Scan(
		&result.ObjectID,
		&result.AssetID,
		&result.StorageBackend,
		&result.StorageKey,
		&result.MIMEType,
		&container,
		&sampleRate,
		&result.Size,
		&result.SHA256,
		&result.DurationMS,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Original{}, ErrAudioNotFound
	}
	if err != nil {
		return Original{}, fmt.Errorf("query original audio: %w", err)
	}
	if container != nil {
		result.Container = *container
	}
	if sampleRate != nil {
		result.SampleRate = *sampleRate
	}
	return result, nil
}
