package glossary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Create(ctx context.Context, params CreateParams) (Set, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Set{}, fmt.Errorf("begin glossary set transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if params.ScopeType == ScopeAsset {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM assets
				WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
			)`, params.ScopeID, params.WorkspaceID).Scan(&exists); err != nil {
			return Set{}, fmt.Errorf("validate glossary asset scope: %w", err)
		}
		if !exists {
			return Set{}, ErrNotFound
		}
	}
	var result Set
	err = tx.QueryRow(ctx, `
		INSERT INTO glossary_sets (
			id, workspace_id, display_name, scope_type, scope_id,
			state, current_version, row_version, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, 1, 1, $7)
		RETURNING id::text, workspace_id::text, display_name, scope_type,
		          scope_id::text, state, current_version, row_version,
		          created_at, updated_at`,
		params.SetID, params.WorkspaceID, params.DisplayName, params.ScopeType,
		params.ScopeID, params.State, params.CreatedBy,
	).Scan(
		&result.ID, &result.WorkspaceID, &result.DisplayName, &result.ScopeType,
		&result.ScopeID, &result.State, &result.CurrentVersion,
		&result.ResourceVersion, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Set{}, ErrConflict
		}
		return Set{}, fmt.Errorf("insert glossary set: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO glossary_set_versions (
			id, glossary_set_id, version, entries, created_by
		) VALUES ($1, $2, 1, $3, $4)`,
		params.VersionID, params.SetID, params.EntriesJSON, params.CreatedBy,
	); err != nil {
		return Set{}, fmt.Errorf("insert initial glossary version: %w", err)
	}
	result.Entries, err = decodeEntries(params.EntriesJSON)
	if err != nil {
		return Set{}, fmt.Errorf("decode inserted glossary entries: %w", err)
	}
	metadata, err := json.Marshal(struct {
		ScopeType string  `json:"scope_type"`
		ScopeID   *string `json:"scope_id,omitempty"`
		State     string  `json:"state"`
		Version   int     `json:"version"`
	}{params.ScopeType, params.ScopeID, params.State, 1})
	if err != nil {
		return Set{}, fmt.Errorf("encode glossary audit metadata: %w", err)
	}
	if err := insertAudit(ctx, tx, params.AuditID, params.WorkspaceID, params.CreatedBy,
		"glossary_set.created", params.SetID, metadata); err != nil {
		return Set{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Set{}, fmt.Errorf("commit glossary set transaction: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) List(ctx context.Context, workspaceID string) ([]Set, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT glossary_set.id::text, glossary_set.workspace_id::text,
		       glossary_set.display_name, glossary_set.scope_type,
		       glossary_set.scope_id::text, glossary_set.state,
		       glossary_set.current_version, glossary_set.row_version,
		       version.entries, glossary_set.created_at, glossary_set.updated_at
		FROM glossary_sets glossary_set
		JOIN glossary_set_versions version
		  ON version.glossary_set_id = glossary_set.id
		 AND version.version = glossary_set.current_version
		WHERE glossary_set.workspace_id = $1
		ORDER BY CASE glossary_set.scope_type
		           WHEN 'workspace' THEN 1 WHEN 'collection' THEN 2 ELSE 3 END,
		         lower(glossary_set.display_name), glossary_set.id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query glossary sets: %w", err)
	}
	defer rows.Close()
	sets := make([]Set, 0)
	for rows.Next() {
		set, err := scanSet(rows)
		if err != nil {
			return nil, err
		}
		sets = append(sets, set)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate glossary sets: %w", err)
	}
	return sets, nil
}

func (repository *PostgresRepository) AddVersion(ctx context.Context, params AddVersionParams) (Set, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Set{}, fmt.Errorf("begin glossary version transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	result, err := lockSet(ctx, tx, params.WorkspaceID, params.SetID)
	if err != nil {
		return Set{}, err
	}
	if result.ResourceVersion != params.ExpectedResourceVersion {
		return Set{}, ErrConflict
	}
	nextVersion := result.CurrentVersion + 1
	if _, err := tx.Exec(ctx, `
		INSERT INTO glossary_set_versions (
			id, glossary_set_id, version, entries, created_by
		) VALUES ($1, $2, $3, $4, $5)`,
		params.VersionID, params.SetID, nextVersion, params.EntriesJSON, params.CreatedBy,
	); err != nil {
		if isUniqueViolation(err) {
			return Set{}, ErrConflict
		}
		return Set{}, fmt.Errorf("insert glossary version: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		UPDATE glossary_sets
		SET current_version = $3, row_version = row_version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2
		RETURNING current_version, row_version, updated_at`,
		params.SetID, params.WorkspaceID, nextVersion,
	).Scan(&result.CurrentVersion, &result.ResourceVersion, &result.UpdatedAt); err != nil {
		return Set{}, fmt.Errorf("publish glossary version: %w", err)
	}
	result.Entries, err = decodeEntries(params.EntriesJSON)
	if err != nil {
		return Set{}, fmt.Errorf("decode published glossary entries: %w", err)
	}
	metadata, err := json.Marshal(struct {
		Version int `json:"version"`
	}{nextVersion})
	if err != nil {
		return Set{}, fmt.Errorf("encode glossary version audit metadata: %w", err)
	}
	if err := insertAudit(ctx, tx, params.AuditID, params.WorkspaceID, params.CreatedBy,
		"glossary_set.version_created", params.SetID, metadata); err != nil {
		return Set{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Set{}, fmt.Errorf("commit glossary version transaction: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) UpdateState(ctx context.Context, params UpdateStateParams) (Set, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Set{}, fmt.Errorf("begin glossary update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	result, err := lockSet(ctx, tx, params.WorkspaceID, params.SetID)
	if err != nil {
		return Set{}, err
	}
	if result.ResourceVersion != params.ExpectedResourceVersion {
		return Set{}, ErrConflict
	}
	if err := tx.QueryRow(ctx, `
		UPDATE glossary_sets
		SET state = $3, row_version = row_version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2
		RETURNING state, row_version, updated_at`,
		params.SetID, params.WorkspaceID, params.State,
	).Scan(&result.State, &result.ResourceVersion, &result.UpdatedAt); err != nil {
		return Set{}, fmt.Errorf("update glossary state: %w", err)
	}
	metadata, err := json.Marshal(struct {
		State string `json:"state"`
	}{params.State})
	if err != nil {
		return Set{}, fmt.Errorf("encode glossary update audit metadata: %w", err)
	}
	if err := insertAudit(ctx, tx, params.AuditID, params.WorkspaceID, params.UpdatedBy,
		"glossary_set.updated", params.SetID, metadata); err != nil {
		return Set{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Set{}, fmt.Errorf("commit glossary update transaction: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) Resolve(ctx context.Context, workspaceID, assetID, defaultSetID string) ([]resolvedSet, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT glossary_set.id::text, glossary_set.scope_type,
		       glossary_set.scope_id::text, glossary_set.current_version,
		       version.entries
		FROM glossary_sets glossary_set
		JOIN glossary_set_versions version
		  ON version.glossary_set_id = glossary_set.id
		 AND version.version = glossary_set.current_version
		WHERE glossary_set.workspace_id = $1
		  AND glossary_set.state = 'enabled'
		  AND (
		      glossary_set.scope_type = 'workspace'
		      OR (glossary_set.scope_type = 'asset' AND glossary_set.scope_id = $2)
		      OR glossary_set.id::text = NULLIF($3, '')
		  )
		ORDER BY CASE glossary_set.scope_type WHEN 'workspace' THEN 1 ELSE 3 END,
		         glossary_set.id`, workspaceID, assetID, defaultSetID)
	if err != nil {
		return nil, fmt.Errorf("query effective glossary sets: %w", err)
	}
	defer rows.Close()
	sets := make([]resolvedSet, 0)
	for rows.Next() {
		var result resolvedSet
		var entriesJSON []byte
		if err := rows.Scan(&result.ID, &result.ScopeType, &result.ScopeID,
			&result.CurrentVersion, &entriesJSON); err != nil {
			return nil, fmt.Errorf("scan effective glossary set: %w", err)
		}
		result.Entries, err = decodeEntries(entriesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode effective glossary entries: %w", err)
		}
		sets = append(sets, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate effective glossary sets: %w", err)
	}
	return sets, nil
}

type rowScanner interface{ Scan(...any) error }

func scanSet(row rowScanner) (Set, error) {
	var result Set
	var entriesJSON []byte
	if err := row.Scan(&result.ID, &result.WorkspaceID, &result.DisplayName,
		&result.ScopeType, &result.ScopeID, &result.State, &result.CurrentVersion,
		&result.ResourceVersion, &entriesJSON, &result.CreatedAt, &result.UpdatedAt); err != nil {
		return Set{}, fmt.Errorf("scan glossary set: %w", err)
	}
	entries, err := decodeEntries(entriesJSON)
	if err != nil {
		return Set{}, fmt.Errorf("decode glossary entries: %w", err)
	}
	result.Entries = entries
	return result, nil
}

func lockSet(ctx context.Context, tx pgx.Tx, workspaceID, setID string) (Set, error) {
	result, err := scanSet(tx.QueryRow(ctx, `
		SELECT glossary_set.id::text, glossary_set.workspace_id::text,
		       glossary_set.display_name, glossary_set.scope_type,
		       glossary_set.scope_id::text, glossary_set.state,
		       glossary_set.current_version, glossary_set.row_version,
		       version.entries, glossary_set.created_at, glossary_set.updated_at
		FROM glossary_sets glossary_set
		JOIN glossary_set_versions version
		  ON version.glossary_set_id = glossary_set.id
		 AND version.version = glossary_set.current_version
		WHERE glossary_set.id = $1 AND glossary_set.workspace_id = $2
		FOR UPDATE OF glossary_set`, setID, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Set{}, ErrNotFound
	}
	return result, err
}

func insertAudit(ctx context.Context, tx pgx.Tx, auditID, workspaceID, actorID, action, setID string, metadata []byte) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action,
			target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', $4, 'glossary_set', $5, $6)`,
		auditID, workspaceID, actorID, action, setID, metadata,
	); err != nil {
		return fmt.Errorf("insert glossary audit: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
