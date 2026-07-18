package workspace

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

func (repository *PostgresRepository) Get(ctx context.Context, workspaceID string) (Workspace, error) {
	result, err := scanWorkspace(repository.pool.QueryRow(ctx, workspaceSelect+` WHERE id = $1`, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("query workspace profile: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) Update(ctx context.Context, params UpdateParams) (Workspace, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Workspace{}, fmt.Errorf("begin workspace update: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	current, err := scanWorkspace(tx.QueryRow(ctx, workspaceSelect+` WHERE id = $1 FOR UPDATE`, params.WorkspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Workspace{}, ErrNotFound
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("lock workspace profile: %w", err)
	}
	if current.Version != params.ExpectedVersion {
		return Workspace{}, ErrVersionConflict
	}
	if current.Name == params.Name {
		if err := tx.Commit(ctx); err != nil {
			return Workspace{}, fmt.Errorf("commit unchanged workspace: %w", err)
		}
		return current, nil
	}

	updated, err := scanWorkspace(tx.QueryRow(ctx, `
		UPDATE workspaces
		SET name = $2, version = version + 1,
		    updated_at = GREATEST($3, updated_at + interval '1 microsecond')
		WHERE id = $1
		RETURNING id::text, name, version, created_at, updated_at`,
		params.WorkspaceID, params.Name, params.UpdatedAt))
	if err != nil {
		return Workspace{}, fmt.Errorf("persist workspace profile: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, 'user', 'workspace.updated', 'workspace', $2, $4,
			jsonb_build_object(
				'previous_name', $5::text, 'name', $6::text,
				'previous_version', $7::bigint, 'version', $8::bigint
			)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.RequestID,
		current.Name, updated.Name, current.Version, updated.Version); err != nil {
		return Workspace{}, fmt.Errorf("insert workspace update audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Workspace{}, fmt.Errorf("commit workspace update: %w", err)
	}
	return updated, nil
}

const workspaceSelect = `
	SELECT id::text, name, version, created_at, updated_at
	FROM workspaces`

type rowScanner interface {
	Scan(...any) error
}

func scanWorkspace(row rowScanner) (Workspace, error) {
	var result Workspace
	err := row.Scan(&result.ID, &result.Name, &result.Version, &result.CreatedAt, &result.UpdatedAt)
	return result, err
}
