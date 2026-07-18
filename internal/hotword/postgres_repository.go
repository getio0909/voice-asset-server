package hotword

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

func (repository *PostgresRepository) Create(
	ctx context.Context,
	params CreateParams,
) (Set, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Set{}, fmt.Errorf("begin hotword set transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	if params.ScopeType == ScopeAsset {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM assets
				WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
			)`, params.ScopeID, params.WorkspaceID).Scan(&exists); err != nil {
			return Set{}, fmt.Errorf("validate hotword asset scope: %w", err)
		}
		if !exists {
			return Set{}, ErrNotFound
		}
	}
	var result Set
	err = tx.QueryRow(ctx, `
		INSERT INTO hotword_sets (
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
		if hotwordUniqueViolation(err) {
			return Set{}, ErrConflict
		}
		return Set{}, fmt.Errorf("insert hotword set: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO hotword_set_versions (
			id, hotword_set_id, version, entries, created_by
		) VALUES ($1, $2, 1, $3, $4)`,
		params.VersionID, params.SetID, params.EntriesJSON, params.CreatedBy,
	); err != nil {
		return Set{}, fmt.Errorf("insert initial hotword version: %w", err)
	}
	result.Entries, err = decodeEntries(params.EntriesJSON)
	if err != nil {
		return Set{}, fmt.Errorf("decode inserted hotword entries: %w", err)
	}
	metadata, err := json.Marshal(struct {
		ScopeType string  `json:"scope_type"`
		ScopeID   *string `json:"scope_id,omitempty"`
		State     string  `json:"state"`
		Version   int     `json:"version"`
	}{params.ScopeType, params.ScopeID, params.State, 1})
	if err != nil {
		return Set{}, fmt.Errorf("encode hotword audit metadata: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action,
			target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', 'hotword_set.created', 'hotword_set', $4, $5)`,
		params.AuditID, params.WorkspaceID, params.CreatedBy, params.SetID, metadata,
	); err != nil {
		return Set{}, fmt.Errorf("insert hotword creation audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Set{}, fmt.Errorf("commit hotword set transaction: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) List(
	ctx context.Context,
	workspaceID string,
) ([]Set, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT hotword_set.id::text, hotword_set.workspace_id::text,
		       hotword_set.display_name, hotword_set.scope_type,
		       hotword_set.scope_id::text, hotword_set.state,
		       hotword_set.current_version, hotword_set.row_version,
		       version.entries, hotword_set.created_at, hotword_set.updated_at
		FROM hotword_sets hotword_set
		JOIN hotword_set_versions version
		  ON version.hotword_set_id = hotword_set.id
		 AND version.version = hotword_set.current_version
		WHERE hotword_set.workspace_id = $1
		ORDER BY CASE hotword_set.scope_type
		           WHEN 'workspace' THEN 1 WHEN 'collection' THEN 2 ELSE 3 END,
		         lower(hotword_set.display_name), hotword_set.id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query hotword sets: %w", err)
	}
	defer rows.Close()
	sets := make([]Set, 0)
	for rows.Next() {
		set, err := scanHotwordSet(rows)
		if err != nil {
			return nil, err
		}
		sets = append(sets, set)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hotword sets: %w", err)
	}
	return sets, nil
}

func (repository *PostgresRepository) AddVersion(
	ctx context.Context,
	params AddVersionParams,
) (Set, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Set{}, fmt.Errorf("begin hotword version transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	result, err := lockHotwordSet(ctx, tx, params.WorkspaceID, params.SetID)
	if err != nil {
		return Set{}, err
	}
	if result.ResourceVersion != params.ExpectedResourceVersion {
		return Set{}, ErrConflict
	}
	nextVersion := result.CurrentVersion + 1
	if _, err := tx.Exec(ctx, `
		INSERT INTO hotword_set_versions (
			id, hotword_set_id, version, entries, created_by
		) VALUES ($1, $2, $3, $4, $5)`,
		params.VersionID, params.SetID, nextVersion, params.EntriesJSON, params.CreatedBy,
	); err != nil {
		if hotwordUniqueViolation(err) {
			return Set{}, ErrConflict
		}
		return Set{}, fmt.Errorf("insert hotword set version: %w", err)
	}
	if err := tx.QueryRow(ctx, `
		UPDATE hotword_sets
		SET current_version = $3, row_version = row_version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2
		RETURNING current_version, row_version, updated_at`,
		params.SetID, params.WorkspaceID, nextVersion,
	).Scan(&result.CurrentVersion, &result.ResourceVersion, &result.UpdatedAt); err != nil {
		return Set{}, fmt.Errorf("publish hotword set version: %w", err)
	}
	result.Entries, err = decodeEntries(params.EntriesJSON)
	if err != nil {
		return Set{}, fmt.Errorf("decode published hotword entries: %w", err)
	}
	metadata, err := json.Marshal(struct {
		Version int `json:"version"`
	}{Version: nextVersion})
	if err != nil {
		return Set{}, fmt.Errorf("encode hotword version audit metadata: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action,
			target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', 'hotword_set.version_created', 'hotword_set', $4, $5)`,
		params.AuditID, params.WorkspaceID, params.CreatedBy, params.SetID, metadata,
	); err != nil {
		return Set{}, fmt.Errorf("insert hotword version audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Set{}, fmt.Errorf("commit hotword version transaction: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) UpdateState(
	ctx context.Context,
	params UpdateStateParams,
) (Set, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Set{}, fmt.Errorf("begin hotword update transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	result, err := lockHotwordSet(ctx, tx, params.WorkspaceID, params.SetID)
	if err != nil {
		return Set{}, err
	}
	if result.ResourceVersion != params.ExpectedResourceVersion {
		return Set{}, ErrConflict
	}
	if err := tx.QueryRow(ctx, `
		UPDATE hotword_sets
		SET state = $3, row_version = row_version + 1,
		    updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2
		RETURNING state, row_version, updated_at`,
		params.SetID, params.WorkspaceID, params.State,
	).Scan(&result.State, &result.ResourceVersion, &result.UpdatedAt); err != nil {
		return Set{}, fmt.Errorf("update hotword set state: %w", err)
	}
	metadata, err := json.Marshal(struct {
		State string `json:"state"`
	}{State: params.State})
	if err != nil {
		return Set{}, fmt.Errorf("encode hotword update audit metadata: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action,
			target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', 'hotword_set.updated', 'hotword_set', $4, $5)`,
		params.AuditID, params.WorkspaceID, params.UpdatedBy, params.SetID, metadata,
	); err != nil {
		return Set{}, fmt.Errorf("insert hotword update audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Set{}, fmt.Errorf("commit hotword update transaction: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) Resolve(
	ctx context.Context,
	workspaceID,
	assetID string,
) ([]resolvedSet, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT hotword_set.id::text, hotword_set.display_name,
		       hotword_set.scope_type, hotword_set.scope_id::text,
		       hotword_set.current_version, version.entries
		FROM hotword_sets hotword_set
		JOIN hotword_set_versions version
		  ON version.hotword_set_id = hotword_set.id
		 AND version.version = hotword_set.current_version
		WHERE hotword_set.workspace_id = $1
		  AND hotword_set.state = 'enabled'
		  AND (
		      hotword_set.scope_type = 'workspace'
		      OR (hotword_set.scope_type = 'asset' AND hotword_set.scope_id = $2)
		  )
		ORDER BY CASE hotword_set.scope_type WHEN 'workspace' THEN 1 ELSE 3 END,
		         hotword_set.id`, workspaceID, assetID)
	if err != nil {
		return nil, fmt.Errorf("query effective hotword sets: %w", err)
	}
	defer rows.Close()
	sets := make([]resolvedSet, 0)
	for rows.Next() {
		var result resolvedSet
		var entriesJSON []byte
		if err := rows.Scan(
			&result.ID, &result.DisplayName, &result.ScopeType,
			&result.ScopeID, &result.CurrentVersion, &entriesJSON,
		); err != nil {
			return nil, fmt.Errorf("scan effective hotword set: %w", err)
		}
		result.Entries, err = decodeEntries(entriesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode effective hotword entries: %w", err)
		}
		sets = append(sets, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate effective hotword sets: %w", err)
	}
	return sets, nil
}

type hotwordRow interface {
	Scan(destinations ...any) error
}

func scanHotwordSet(row hotwordRow) (Set, error) {
	var result Set
	var entriesJSON []byte
	if err := row.Scan(
		&result.ID, &result.WorkspaceID, &result.DisplayName, &result.ScopeType,
		&result.ScopeID, &result.State, &result.CurrentVersion,
		&result.ResourceVersion, &entriesJSON, &result.CreatedAt, &result.UpdatedAt,
	); err != nil {
		return Set{}, fmt.Errorf("scan hotword set: %w", err)
	}
	entries, err := decodeEntries(entriesJSON)
	if err != nil {
		return Set{}, fmt.Errorf("decode hotword entries: %w", err)
	}
	result.Entries = entries
	return result, nil
}

func lockHotwordSet(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID,
	setID string,
) (Set, error) {
	var result Set
	var entriesJSON []byte
	err := tx.QueryRow(ctx, `
		SELECT hotword_set.id::text, hotword_set.workspace_id::text,
		       hotword_set.display_name, hotword_set.scope_type,
		       hotword_set.scope_id::text, hotword_set.state,
		       hotword_set.current_version, hotword_set.row_version,
		       version.entries, hotword_set.created_at, hotword_set.updated_at
		FROM hotword_sets hotword_set
		JOIN hotword_set_versions version
		  ON version.hotword_set_id = hotword_set.id
		 AND version.version = hotword_set.current_version
		WHERE hotword_set.id = $1 AND hotword_set.workspace_id = $2
		FOR UPDATE OF hotword_set`, setID, workspaceID,
	).Scan(
		&result.ID, &result.WorkspaceID, &result.DisplayName, &result.ScopeType,
		&result.ScopeID, &result.State, &result.CurrentVersion,
		&result.ResourceVersion, &entriesJSON, &result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Set{}, ErrNotFound
	}
	if err != nil {
		return Set{}, fmt.Errorf("lock hotword set: %w", err)
	}
	result.Entries, err = decodeEntries(entriesJSON)
	if err != nil {
		return Set{}, fmt.Errorf("decode locked hotword entries: %w", err)
	}
	return result, nil
}

func hotwordUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
