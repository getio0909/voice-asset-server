package artifactreaper

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) ListExpired(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]Artifact, error) {
	if repository == nil || repository.pool == nil || now.IsZero() || limit < 1 || limit > MaxBatchSize {
		return nil, errors.New("invalid expired-artifact query")
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, kind, storage_backend, storage_key,
		       file_size, sha256, expires_at
		FROM (
			SELECT clip.id, clip.workspace_id, 'audio_clip'::text AS kind,
			       object.storage_backend, object.storage_key, object.file_size,
			       object.sha256, clip.expires_at
			FROM audio_clips AS clip
			JOIN asset_objects AS object ON object.id = clip.id AND object.kind = 'clip'
			WHERE clip.expires_at <= $1
			UNION ALL
			SELECT export.id, export.workspace_id, 'transcript_export'::text AS kind,
			       object.storage_backend, object.storage_key, object.file_size,
			       object.sha256, export.expires_at
			FROM transcript_exports AS export
			JOIN asset_objects AS object ON object.id = export.id AND object.kind = 'export'
			WHERE export.expires_at <= $1
		) AS expired
		ORDER BY expires_at, id
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("query expired artifacts: %w", err)
	}
	defer rows.Close()

	artifacts := make([]Artifact, 0, limit)
	for rows.Next() {
		var artifact Artifact
		if err := rows.Scan(
			&artifact.ID, &artifact.WorkspaceID, &artifact.Kind,
			&artifact.StorageBackend, &artifact.StorageKey, &artifact.FileSize,
			&artifact.SHA256, &artifact.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan expired artifact: %w", err)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired artifacts: %w", err)
	}
	return artifacts, nil
}

func (repository *PostgresRepository) DeleteExpired(
	ctx context.Context,
	artifact Artifact,
	auditID string,
	now time.Time,
) (bool, error) {
	if repository == nil || repository.pool == nil || auditID == "" || now.IsZero() {
		return false, errors.New("invalid expired-artifact deletion")
	}
	query, objectKind, err := deleteQuery(artifact.Kind)
	if err != nil {
		return false, err
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin expired-artifact deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	command, err := tx.Exec(
		ctx, query, artifact.ID, artifact.WorkspaceID, now, artifact.StorageBackend,
		artifact.StorageKey, artifact.FileSize, artifact.SHA256,
	)
	if err != nil {
		return false, fmt.Errorf("delete expired artifact metadata: %w", err)
	}
	if command.RowsAffected() == 0 {
		return false, nil
	}
	if command.RowsAffected() != 1 {
		return false, errors.New("expired-artifact deletion affected multiple rows")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata, occurred_at
		) VALUES (
			$1, $2, NULL, 'system', 'artifact.reaped', $3, $4, NULL,
			jsonb_build_object(
				'artifact_kind', $5::text, 'expires_at', $6::timestamptz,
				'file_size', $7::bigint
			), $8
		)`, auditID, artifact.WorkspaceID, artifact.Kind, artifact.ID, objectKind,
		artifact.ExpiresAt, artifact.FileSize, now); err != nil {
		return false, fmt.Errorf("audit expired-artifact deletion: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit expired-artifact deletion: %w", err)
	}
	return true, nil
}

func deleteQuery(kind string) (string, string, error) {
	switch kind {
	case KindAudioClip:
		return `
			DELETE FROM asset_objects AS object
			USING audio_clips AS artifact
			WHERE object.id = $1 AND artifact.id = object.id
			  AND artifact.workspace_id = $2 AND artifact.expires_at <= $3
			  AND object.kind = 'clip' AND object.storage_backend = $4
			  AND object.storage_key = $5 AND object.file_size = $6
			  AND object.sha256 = $7`, "clip", nil
	case KindTranscriptExport:
		return `
			DELETE FROM asset_objects AS object
			USING transcript_exports AS artifact
			WHERE object.id = $1 AND artifact.id = object.id
			  AND artifact.workspace_id = $2 AND artifact.expires_at <= $3
			  AND object.kind = 'export' AND object.storage_backend = $4
			  AND object.storage_key = $5 AND object.file_size = $6
			  AND object.sha256 = $7`, "export", nil
	default:
		return "", "", errors.New("artifact kind is not reaper-managed")
	}
}
