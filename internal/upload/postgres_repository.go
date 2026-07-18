package upload

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, params CreateParams) (Session, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Session{}, false, fmt.Errorf("begin upload transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var assetState string
	if err := tx.QueryRow(ctx, `
		SELECT status
		FROM assets
		WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
		FOR UPDATE`, params.AssetID, params.WorkspaceID).Scan(&assetState); errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, ErrNotFound
	} else if err != nil {
		return Session{}, false, fmt.Errorf("lock upload asset: %w", err)
	}
	var existingHash string
	existing, err := scanSessionWithHash(tx.QueryRow(ctx, `
		SELECT id::text, asset_id::text, workspace_id::text, filename, mime_type,
		       expected_size, expected_sha256, part_size, state, expires_at,
		       created_at, updated_at, completed_at, error_code, idempotency_request_hash
		FROM upload_sessions
		WHERE workspace_id = $1 AND idempotency_key = $2`,
		params.WorkspaceID, params.IdempotencyKey,
	), &existingHash)
	if err == nil {
		if existingHash != params.RequestHash {
			return Session{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, fmt.Errorf("query idempotent upload: %w", err)
	}
	if assetState != "draft" && assetState != "uploading" {
		return Session{}, false, ErrStateConflict
	}
	var hasOpenUpload bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM upload_sessions
			WHERE asset_id = $1 AND workspace_id = $2 AND state IN ('active', 'assembling')
		)`, params.AssetID, params.WorkspaceID).Scan(&hasOpenUpload); err != nil {
		return Session{}, false, fmt.Errorf("query open upload: %w", err)
	}
	if hasOpenUpload {
		return Session{}, false, ErrStateConflict
	}

	created, err := scanSession(tx.QueryRow(ctx, `
		INSERT INTO upload_sessions (
			id, workspace_id, asset_id, created_by, filename, mime_type,
			expected_size, expected_sha256, part_size, idempotency_key,
			idempotency_request_hash, state, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'active', $12)
		RETURNING id::text, asset_id::text, workspace_id::text, filename, mime_type,
		          expected_size, expected_sha256, part_size, state, expires_at,
		          created_at, updated_at, completed_at, error_code`,
		params.SessionID,
		params.WorkspaceID,
		params.AssetID,
		params.CreatedBy,
		params.Filename,
		params.MIMEType,
		params.ExpectedSize,
		params.ExpectedSHA256,
		params.PartSize,
		params.IdempotencyKey,
		params.RequestHash,
		params.ExpiresAt,
	))
	if err != nil {
		return Session{}, false, fmt.Errorf("insert upload session: %w", err)
	}
	if assetState == "draft" {
		if _, err := tx.Exec(ctx, `
			UPDATE assets
			SET status = 'uploading', version = version + 1, updated_at = clock_timestamp()
			WHERE id = $1 AND workspace_id = $2`, params.AssetID, params.WorkspaceID); err != nil {
			return Session{}, false, fmt.Errorf("mark asset uploading: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', 'upload.started', 'upload_session', $4, '{}')`,
		params.AuditID, params.WorkspaceID, params.CreatedBy, params.SessionID,
	); err != nil {
		return Session{}, false, fmt.Errorf("insert upload audit log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, false, fmt.Errorf("commit upload transaction: %w", err)
	}
	return created, false, nil
}

func (r *PostgresRepository) Get(ctx context.Context, workspaceID, uploadID string) (Session, []Part, error) {
	session, err := scanSession(r.pool.QueryRow(ctx, `
		SELECT id::text, asset_id::text, workspace_id::text, filename, mime_type,
		       expected_size, expected_sha256, part_size, state, expires_at,
		       created_at, updated_at, completed_at, error_code
		FROM upload_sessions
		WHERE id = $1 AND workspace_id = $2`, uploadID, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, nil, ErrNotFound
	}
	if err != nil {
		return Session{}, nil, fmt.Errorf("query upload session: %w", err)
	}
	rows, err := r.pool.Query(ctx, `
		SELECT part_number, size_bytes, sha256, storage_key, created_at
		FROM upload_parts
		WHERE upload_session_id = $1
		ORDER BY part_number`, uploadID)
	if err != nil {
		return Session{}, nil, fmt.Errorf("query upload parts: %w", err)
	}
	defer rows.Close()
	parts := make([]Part, 0)
	for rows.Next() {
		var part Part
		if err := rows.Scan(&part.Number, &part.SizeBytes, &part.SHA256, &part.StorageKey, &part.CreatedAt); err != nil {
			return Session{}, nil, fmt.Errorf("scan upload part: %w", err)
		}
		parts = append(parts, part)
	}
	if err := rows.Err(); err != nil {
		return Session{}, nil, fmt.Errorf("iterate upload parts: %w", err)
	}
	session.Parts = parts
	return session, parts, nil
}

func (r *PostgresRepository) RecordPart(ctx context.Context, params RecordPartParams) (Part, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Part{}, false, fmt.Errorf("begin part transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var state string
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT state, expires_at > clock_timestamp()
		FROM upload_sessions
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, params.UploadID, params.WorkspaceID).Scan(&state, &active); errors.Is(err, pgx.ErrNoRows) {
		return Part{}, false, ErrNotFound
	} else if err != nil {
		return Part{}, false, fmt.Errorf("lock upload session: %w", err)
	}
	if state != StateActive {
		return Part{}, false, ErrStateConflict
	}
	if !active {
		return Part{}, false, ErrExpired
	}
	var result Part
	err = tx.QueryRow(ctx, `
		INSERT INTO upload_parts (upload_session_id, part_number, storage_key, size_bytes, sha256)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (upload_session_id, part_number) DO NOTHING
		RETURNING part_number, size_bytes, sha256, storage_key, created_at`,
		params.UploadID,
		params.Part.Number,
		params.Part.StorageKey,
		params.Part.SizeBytes,
		params.Part.SHA256,
	).Scan(&result.Number, &result.SizeBytes, &result.SHA256, &result.StorageKey, &result.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.QueryRow(ctx, `
			SELECT part_number, size_bytes, sha256, storage_key, created_at
			FROM upload_parts
			WHERE upload_session_id = $1 AND part_number = $2`,
			params.UploadID, params.Part.Number,
		).Scan(&result.Number, &result.SizeBytes, &result.SHA256, &result.StorageKey, &result.CreatedAt); err != nil {
			return Part{}, false, fmt.Errorf("load existing upload part: %w", err)
		}
		if result.SizeBytes != params.Part.SizeBytes || result.SHA256 != params.Part.SHA256 || result.StorageKey != params.Part.StorageKey {
			return Part{}, false, ErrPartConflict
		}
		return result, true, nil
	}
	if err != nil {
		return Part{}, false, fmt.Errorf("insert upload part: %w", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE upload_sessions SET updated_at = clock_timestamp() WHERE id = $1", params.UploadID); err != nil {
		return Part{}, false, fmt.Errorf("touch upload session: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Part{}, false, fmt.Errorf("commit part transaction: %w", err)
	}
	return result, false, nil
}

func (r *PostgresRepository) MarkAssembling(ctx context.Context, workspaceID, uploadID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE upload_sessions
		SET state = 'assembling', updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2
		  AND state = 'active' AND expires_at > clock_timestamp()`, uploadID, workspaceID)
	if err != nil {
		return fmt.Errorf("mark upload assembling: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return r.classifyTransition(ctx, workspaceID, uploadID)
}

func (r *PostgresRepository) ResetToActive(ctx context.Context, workspaceID, uploadID string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE upload_sessions
		SET state = 'active', error_code = NULL, updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2 AND state = 'assembling'`, uploadID, workspaceID)
	if err != nil {
		return fmt.Errorf("reset upload active: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrStateConflict
	}
	return nil
}

func (r *PostgresRepository) Finish(ctx context.Context, params FinishParams) (Session, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin completion transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var assetID, state string
	if err := tx.QueryRow(ctx, `
		SELECT asset_id::text, state
		FROM upload_sessions
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, params.UploadID, params.WorkspaceID).Scan(&assetID, &state); errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	} else if err != nil {
		return Session{}, fmt.Errorf("lock upload completion: %w", err)
	}
	if state != StateAssembling {
		return Session{}, ErrStateConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			container, codec, sample_rate, channel_count, bitrate, duration_ms,
			file_size, sha256, creation_source, encryption_state
		) VALUES (
			$1, $2, 'original', $3, $4, $5,
			$6, $7, $8, $9, $10, $11,
			$12, $13, 'upload', 'none'
		)`,
		params.Object.ID,
		assetID,
		params.Object.StorageBackend,
		params.Object.StorageKey,
		params.Object.MIMEType,
		params.Object.Container,
		params.Object.Codec,
		params.Object.SampleRate,
		params.Object.ChannelCount,
		params.Object.Bitrate,
		params.Object.DurationMS,
		params.Object.FileSize,
		params.Object.SHA256,
	); err != nil {
		return Session{}, fmt.Errorf("insert original object: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE assets
		SET status = 'ready', duration_ms = $3, version = version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2`, assetID, params.WorkspaceID, params.Object.DurationMS); err != nil {
		return Session{}, fmt.Errorf("mark asset ready: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO jobs (
			id, workspace_id, asset_id, created_by, kind, state, payload, max_attempts
		) VALUES (
			$1, $2, $3, $4, 'generate_waveform', 'queued',
			jsonb_build_object('asset_id', $3::uuid::text), 3
		)`, params.WaveformJobID, params.WorkspaceID, assetID, params.ActorID); err != nil {
		return Session{}, fmt.Errorf("enqueue waveform generation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE upload_sessions
		SET state = 'completed', error_code = NULL, completed_at = clock_timestamp(),
		    updated_at = clock_timestamp()
		WHERE id = $1`, params.UploadID); err != nil {
		return Session{}, fmt.Errorf("mark upload completed: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', 'upload.completed', 'asset', $4, '{}')`,
		params.AuditID, params.WorkspaceID, params.ActorID, assetID,
	); err != nil {
		return Session{}, fmt.Errorf("insert completion audit log: %w", err)
	}
	completed, err := scanSession(tx.QueryRow(ctx, `
		SELECT id::text, asset_id::text, workspace_id::text, filename, mime_type,
		       expected_size, expected_sha256, part_size, state, expires_at,
		       created_at, updated_at, completed_at, error_code
		FROM upload_sessions WHERE id = $1`, params.UploadID))
	if err != nil {
		return Session{}, fmt.Errorf("load completed upload: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit upload completion: %w", err)
	}
	return completed, nil
}

func (r *PostgresRepository) MarkFailed(ctx context.Context, params FailureParams) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin upload failure transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var assetID, state string
	if err := tx.QueryRow(ctx, `
		SELECT asset_id::text, state
		FROM upload_sessions
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, params.UploadID, params.WorkspaceID).Scan(&assetID, &state); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("lock failed upload: %w", err)
	}
	if state != StateAssembling {
		return ErrStateConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE upload_sessions
		SET state = 'failed', error_code = $2, updated_at = clock_timestamp()
		WHERE id = $1`, params.UploadID, params.ErrorCode); err != nil {
		return fmt.Errorf("mark upload failed: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE assets
		SET status = 'failed', version = version + 1, updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2`, assetID, params.WorkspaceID); err != nil {
		return fmt.Errorf("mark asset failed: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES (
			$1, $2, $3, 'user', 'upload.failed', 'upload_session', $4,
			jsonb_build_object('error_code', $5::text)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.UploadID, params.ErrorCode); err != nil {
		return fmt.Errorf("insert upload failure audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit upload failure: %w", err)
	}
	return nil
}

func (r *PostgresRepository) classifyTransition(ctx context.Context, workspaceID, uploadID string) error {
	var state string
	var active bool
	if err := r.pool.QueryRow(ctx, `
		SELECT state, expires_at > clock_timestamp()
		FROM upload_sessions
		WHERE id = $1 AND workspace_id = $2`, uploadID, workspaceID).Scan(&state, &active); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("classify upload state: %w", err)
	}
	if !active && state == StateActive {
		return ErrExpired
	}
	return ErrStateConflict
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(row rowScanner) (Session, error) {
	var result Session
	err := row.Scan(
		&result.ID,
		&result.AssetID,
		&result.WorkspaceID,
		&result.Filename,
		&result.MIMEType,
		&result.ExpectedSize,
		&result.ExpectedSHA256,
		&result.PartSize,
		&result.State,
		&result.ExpiresAt,
		&result.CreatedAt,
		&result.UpdatedAt,
		&result.CompletedAt,
		&result.ErrorCode,
	)
	return result, err
}

func scanSessionWithHash(row rowScanner, requestHash *string) (Session, error) {
	var result Session
	err := row.Scan(
		&result.ID,
		&result.AssetID,
		&result.WorkspaceID,
		&result.Filename,
		&result.MIMEType,
		&result.ExpectedSize,
		&result.ExpectedSHA256,
		&result.PartSize,
		&result.State,
		&result.ExpiresAt,
		&result.CreatedAt,
		&result.UpdatedAt,
		&result.CompletedAt,
		&result.ErrorCode,
		requestHash,
	)
	return result, err
}
