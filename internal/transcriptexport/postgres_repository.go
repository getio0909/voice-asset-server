package transcriptexport

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

func (repository *PostgresRepository) Create(ctx context.Context, params CreateParams) (Export, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Export{}, fmt.Errorf("begin transcript export creation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var sourceExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM transcript_revisions AS revision
			JOIN transcripts AS transcript ON transcript.id = revision.transcript_id
			JOIN assets AS asset ON asset.id = transcript.asset_id
			WHERE revision.id = $1 AND transcript.asset_id = $2
			  AND asset.workspace_id = $3 AND asset.deleted_at IS NULL
		)`, params.RevisionID, params.AssetID, params.WorkspaceID,
	).Scan(&sourceExists); err != nil {
		return Export{}, fmt.Errorf("check transcript export source: %w", err)
	}
	if !sourceExists {
		return Export{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			file_size, sha256, creation_source, encryption_state
		) VALUES ($1, $2, 'export', $3, $4, $5, $6, $7, 'transcript_export', 'none')`,
		params.ID, params.AssetID, params.StorageBackend, params.StorageKey, params.MIMEType,
		params.FileSize, params.SHA256,
	); err != nil {
		return Export{}, fmt.Errorf("insert transcript export object: %w", err)
	}
	var created Export
	if err := tx.QueryRow(ctx, `
		INSERT INTO transcript_exports (
			id, workspace_id, asset_id, revision_id, format, created_by, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, asset_id::text, revision_id::text, format, created_at, expires_at`,
		params.ID, params.WorkspaceID, params.AssetID, params.RevisionID,
		params.Format, params.ActorID, params.ExpiresAt,
	).Scan(
		&created.ID, &created.AssetID, &created.RevisionID, &created.Format,
		&created.CreatedAt, &created.ExpiresAt,
	); err != nil {
		return Export{}, fmt.Errorf("insert transcript export record: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, $4, 'transcript_export.created', 'transcript_export', $5, $6,
			jsonb_build_object(
				'asset_id', $7::uuid, 'revision_id', $8::uuid, 'format', $9::text,
				'api_key_id', NULLIF($10::text, '')
			)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.ActorType,
		params.ID, params.RequestID, params.AssetID, params.RevisionID, params.Format,
		params.CredentialID,
	); err != nil {
		return Export{}, fmt.Errorf("insert transcript export audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Export{}, fmt.Errorf("commit transcript export creation: %w", err)
	}
	created.MIMEType = params.MIMEType
	created.FileSize = params.FileSize
	created.SHA256 = params.SHA256
	return created, nil
}

func (repository *PostgresRepository) Get(
	ctx context.Context,
	workspaceID,
	exportID string,
	now time.Time,
) (StoredExport, error) {
	var result StoredExport
	err := repository.pool.QueryRow(ctx, `
		SELECT export.id::text, export.asset_id::text, export.revision_id::text,
		       export.format, object.mime_type, object.file_size, object.sha256,
		       object.storage_backend, object.storage_key, export.created_at, export.expires_at
		FROM transcript_exports AS export
		JOIN asset_objects AS object ON object.id = export.id
		JOIN assets AS asset ON asset.id = export.asset_id
		WHERE export.workspace_id = $1 AND export.id = $2 AND export.expires_at > $3
		  AND asset.deleted_at IS NULL`, workspaceID, exportID, now,
	).Scan(
		&result.ID, &result.AssetID, &result.RevisionID, &result.Format,
		&result.MIMEType, &result.FileSize, &result.SHA256, &result.StorageBackend, &result.StorageKey,
		&result.CreatedAt, &result.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return StoredExport{}, ErrNotFound
	}
	if err != nil {
		return StoredExport{}, fmt.Errorf("query transcript export: %w", err)
	}
	result.DownloadURL = "/api/v1/transcript-exports/" + result.ID
	return result, nil
}
