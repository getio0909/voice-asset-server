package apikey

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

func (repository *PostgresRepository) Create(ctx context.Context, params CreateParams) (APIKey, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, fmt.Errorf("begin API key transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	created, err := scanAPIKey(tx.QueryRow(ctx, `
		INSERT INTO api_keys (
			id, workspace_id, created_by, name, token_prefix, token_hash, scopes, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text, workspace_id::text, name, token_prefix, scopes,
		          expires_at, revoked_at, last_used_at, created_at`,
		params.ID, params.WorkspaceID, params.CreatedBy, params.Name, params.TokenPrefix,
		params.TokenHash, params.Scopes, params.ExpiresAt,
	))
	if err != nil {
		return APIKey{}, fmt.Errorf("insert API key: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, $4, 'api_key.created', 'api_key', $5, $6,
			jsonb_build_object('name', $7::text, 'scopes', $8::text[], 'expires_at', $9::timestamptz)
		)`,
		params.AuditID, params.WorkspaceID, params.CreatedBy, params.ActorType,
		params.ID, params.RequestID, params.Name, params.Scopes, params.ExpiresAt,
	); err != nil {
		return APIKey{}, fmt.Errorf("insert API key audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, fmt.Errorf("commit API key transaction: %w", err)
	}
	return created, nil
}

func (repository *PostgresRepository) List(ctx context.Context, workspaceID string) ([]APIKey, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, workspace_id::text, name, token_prefix, scopes,
		       expires_at, revoked_at, last_used_at, created_at
		FROM api_keys
		WHERE workspace_id = $1
		ORDER BY created_at DESC, id DESC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query API keys: %w", err)
	}
	defer rows.Close()
	results := make([]APIKey, 0)
	for rows.Next() {
		result, scanErr := scanAPIKey(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan API key: %w", scanErr)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate API keys: %w", err)
	}
	return results, nil
}

func (repository *PostgresRepository) Revoke(ctx context.Context, params RevokeParams) (APIKey, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, fmt.Errorf("begin API key revocation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	revoked, err := scanAPIKey(tx.QueryRow(ctx, `
		UPDATE api_keys
		SET revoked_at = GREATEST($3, created_at)
		WHERE id = $1 AND workspace_id = $2 AND revoked_at IS NULL
		RETURNING id::text, workspace_id::text, name, token_prefix, scopes,
		          expires_at, revoked_at, last_used_at, created_at`,
		params.ID, params.WorkspaceID, params.RevokedAt,
	))
	newlyRevoked := err == nil
	if errors.Is(err, pgx.ErrNoRows) {
		revoked, err = scanAPIKey(tx.QueryRow(ctx, `
			SELECT id::text, workspace_id::text, name, token_prefix, scopes,
			       expires_at, revoked_at, last_used_at, created_at
			FROM api_keys
			WHERE id = $1 AND workspace_id = $2`, params.ID, params.WorkspaceID))
		if errors.Is(err, pgx.ErrNoRows) {
			return APIKey{}, ErrNotFound
		}
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("revoke API key: %w", err)
	}
	if newlyRevoked {
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (
				id, workspace_id, actor_id, actor_type, action, target_type,
				target_id, request_id, metadata
			) VALUES ($1, $2, $3, $4, 'api_key.revoked', 'api_key', $5, $6, '{}')`,
			params.AuditID, params.WorkspaceID, params.ActorID, params.ActorType,
			params.ID, params.RequestID,
		); err != nil {
			return APIKey{}, fmt.Errorf("insert API key revocation audit: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return APIKey{}, fmt.Errorf("commit API key revocation: %w", err)
	}
	return revoked, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanAPIKey(row rowScanner) (APIKey, error) {
	var result APIKey
	err := row.Scan(
		&result.ID,
		&result.WorkspaceID,
		&result.Name,
		&result.TokenPrefix,
		&result.Scopes,
		&result.ExpiresAt,
		&result.RevokedAt,
		&result.LastUsedAt,
		&result.CreatedAt,
	)
	return result, err
}
