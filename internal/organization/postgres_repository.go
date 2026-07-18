package organization

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

func (repository *PostgresRepository) GetCollection(
	ctx context.Context,
	workspaceID,
	collectionID string,
) (Collection, error) {
	var result Collection
	err := repository.pool.QueryRow(ctx, `
		SELECT collection.id::text, collection.workspace_id::text,
		       collection.name, collection.description, collection.version,
		       count(asset.id) FILTER (WHERE asset.deleted_at IS NULL),
		       collection.created_at, collection.updated_at
		FROM collections AS collection
		LEFT JOIN assets AS asset
		  ON asset.workspace_id = collection.workspace_id
		 AND asset.collection_id = collection.id
		WHERE collection.workspace_id = $1 AND collection.id = $2
		GROUP BY collection.id`, workspaceID, collectionID).Scan(
		&result.ID, &result.WorkspaceID, &result.Name, &result.Description,
		&result.Version, &result.AssetCount, &result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Collection{}, ErrNotFound
	}
	if err != nil {
		return Collection{}, fmt.Errorf("query collection: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) ListCollections(
	ctx context.Context,
	params ListParams,
) ([]Collection, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT collection.id::text, collection.workspace_id::text,
		       collection.name, collection.description, collection.version,
		       count(asset.id) FILTER (WHERE asset.deleted_at IS NULL),
		       collection.created_at, collection.updated_at
		FROM collections AS collection
		LEFT JOIN assets AS asset
		  ON asset.workspace_id = collection.workspace_id
		 AND asset.collection_id = collection.id
		WHERE collection.workspace_id = $1
		  AND ($2::timestamptz IS NULL OR
		       (collection.created_at, collection.id) < ($2, $3::uuid))
		GROUP BY collection.id
		ORDER BY collection.created_at DESC, collection.id DESC
		LIMIT $4`, params.WorkspaceID, nullableTime(params.BeforeCreatedAt), nullableID(params.BeforeID), params.Limit)
	if err != nil {
		return nil, fmt.Errorf("query collections: %w", err)
	}
	defer rows.Close()
	results := make([]Collection, 0)
	for rows.Next() {
		var result Collection
		if err := rows.Scan(
			&result.ID, &result.WorkspaceID, &result.Name, &result.Description,
			&result.Version, &result.AssetCount, &result.CreatedAt, &result.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan collection: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collections: %w", err)
	}
	return results, nil
}

func (repository *PostgresRepository) ListTags(ctx context.Context, params ListParams) ([]Tag, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT tag.id::text, tag.workspace_id::text, tag.name, tag.color,
		       count(asset.id) FILTER (WHERE asset.deleted_at IS NULL), tag.created_at
		FROM tags AS tag
		LEFT JOIN asset_tags AS assignment
		  ON assignment.workspace_id = tag.workspace_id
		 AND assignment.tag_id = tag.id
		LEFT JOIN assets AS asset
		  ON asset.workspace_id = assignment.workspace_id
		 AND asset.id = assignment.asset_id
		WHERE tag.workspace_id = $1
		  AND ($2::timestamptz IS NULL OR
		       (tag.created_at, tag.id) < ($2, $3::uuid))
		GROUP BY tag.id
		ORDER BY tag.created_at DESC, tag.id DESC
		LIMIT $4`, params.WorkspaceID, nullableTime(params.BeforeCreatedAt), nullableID(params.BeforeID), params.Limit)
	if err != nil {
		return nil, fmt.Errorf("query tags: %w", err)
	}
	defer rows.Close()
	results := make([]Tag, 0)
	for rows.Next() {
		var result Tag
		if err := rows.Scan(
			&result.ID, &result.WorkspaceID, &result.Name, &result.Color,
			&result.AssetCount, &result.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tags: %w", err)
	}
	return results, nil
}

func (repository *PostgresRepository) ListAssetTags(
	ctx context.Context,
	params AssetTagListParams,
) ([]Tag, error) {
	var exists bool
	if err := repository.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM assets
			WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		)`, params.WorkspaceID, params.AssetID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check asset for assigned tags: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT tag.id::text, tag.workspace_id::text, tag.name, tag.color,
		       (
		           SELECT count(*)
		           FROM asset_tags AS counted_assignment
		           JOIN assets AS counted_asset
		             ON counted_asset.workspace_id = counted_assignment.workspace_id
		            AND counted_asset.id = counted_assignment.asset_id
		            AND counted_asset.deleted_at IS NULL
		           WHERE counted_assignment.workspace_id = tag.workspace_id
		             AND counted_assignment.tag_id = tag.id
		       ),
		       tag.created_at
		FROM asset_tags AS assignment
		JOIN tags AS tag
		  ON tag.workspace_id = assignment.workspace_id
		 AND tag.id = assignment.tag_id
		WHERE assignment.workspace_id = $1
		  AND assignment.asset_id = $2
		  AND ($3::timestamptz IS NULL OR
		       (tag.created_at, tag.id) < ($3, $4::uuid))
		ORDER BY tag.created_at DESC, tag.id DESC
		LIMIT $5`, params.WorkspaceID, params.AssetID, nullableTime(params.BeforeCreatedAt), nullableID(params.BeforeID), params.Limit)
	if err != nil {
		return nil, fmt.Errorf("query assigned asset tags: %w", err)
	}
	defer rows.Close()
	results := make([]Tag, 0)
	for rows.Next() {
		var result Tag
		if err := rows.Scan(
			&result.ID, &result.WorkspaceID, &result.Name, &result.Color,
			&result.AssetCount, &result.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan assigned asset tag: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assigned asset tags: %w", err)
	}
	return results, nil
}

func (repository *PostgresRepository) ListAnnotations(
	ctx context.Context,
	params AnnotationListParams,
) ([]Annotation, error) {
	var exists bool
	if err := repository.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM assets
			WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		)`, params.WorkspaceID, params.AssetID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check annotation asset: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT annotation.id::text, annotation.workspace_id::text,
		       annotation.asset_id::text, annotation.kind, annotation.start_ms,
		       annotation.end_ms, annotation.body, annotation.version,
		       annotation.created_by::text, annotation.created_at, annotation.updated_at
		FROM annotations AS annotation
		WHERE annotation.workspace_id = $1
		  AND annotation.asset_id = $2
		  AND annotation.deleted_at IS NULL
		  AND ($3::timestamptz IS NULL OR
		       (annotation.created_at, annotation.id) < ($3, $4::uuid))
		ORDER BY annotation.created_at DESC, annotation.id DESC
		LIMIT $5`, params.WorkspaceID, params.AssetID, nullableTime(params.BeforeCreatedAt), nullableID(params.BeforeID), params.Limit)
	if err != nil {
		return nil, fmt.Errorf("query annotations: %w", err)
	}
	defer rows.Close()
	results := make([]Annotation, 0)
	for rows.Next() {
		var result Annotation
		if err := rows.Scan(
			&result.ID, &result.WorkspaceID, &result.AssetID, &result.Kind,
			&result.StartMS, &result.EndMS, &result.Body, &result.Version,
			&result.CreatedBy, &result.CreatedAt, &result.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan annotation: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate annotations: %w", err)
	}
	return results, nil
}

func (repository *PostgresRepository) GetProcessingStatus(
	ctx context.Context,
	workspaceID,
	assetID string,
) (ProcessingStatus, error) {
	result := ProcessingStatus{AssetID: assetID, Jobs: make([]ProcessingJob, 0)}
	if err := repository.pool.QueryRow(ctx, `
		SELECT status, updated_at FROM assets
		WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL`, workspaceID, assetID,
	).Scan(&result.AssetStatus, &result.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return ProcessingStatus{}, ErrNotFound
	} else if err != nil {
		return ProcessingStatus{}, fmt.Errorf("query processing asset: %w", err)
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT id::text, kind, state, attempts, max_attempts, last_error_code,
		       result_revision_id::text, created_at, updated_at
		FROM jobs
		WHERE workspace_id = $1 AND asset_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 20`, workspaceID, assetID)
	if err != nil {
		return ProcessingStatus{}, fmt.Errorf("query processing jobs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item ProcessingJob
		if err := rows.Scan(
			&item.ID, &item.Kind, &item.State, &item.Attempts, &item.MaxAttempts,
			&item.LastErrorCode, &item.ResultRevisionID, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return ProcessingStatus{}, fmt.Errorf("scan processing job: %w", err)
		}
		if item.State == "queued" || item.State == "running" || item.State == "retry_wait" {
			result.Active = true
		}
		if item.UpdatedAt.After(result.UpdatedAt) {
			result.UpdatedAt = item.UpdatedAt
		}
		result.Jobs = append(result.Jobs, item)
	}
	if err := rows.Err(); err != nil {
		return ProcessingStatus{}, fmt.Errorf("iterate processing jobs: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) AddTags(
	ctx context.Context,
	params TagMutationParams,
) (TagMutationResult, error) {
	return repository.mutateTags(ctx, params, true)
}

func (repository *PostgresRepository) RemoveTags(
	ctx context.Context,
	params TagMutationParams,
) (TagMutationResult, error) {
	return repository.mutateTags(ctx, params, false)
}

func (repository *PostgresRepository) mutateTags(
	ctx context.Context,
	params TagMutationParams,
	add bool,
) (TagMutationResult, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return TagMutationResult{}, fmt.Errorf("begin tag mutation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var assetExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM assets
			WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		)`, params.WorkspaceID, params.AssetID).Scan(&assetExists); err != nil {
		return TagMutationResult{}, fmt.Errorf("check tag asset: %w", err)
	}
	if !assetExists {
		return TagMutationResult{}, ErrNotFound
	}
	var tagCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM tags
		WHERE workspace_id = $1 AND id = ANY($2::uuid[])`, params.WorkspaceID, params.TagIDs,
	).Scan(&tagCount); err != nil {
		return TagMutationResult{}, fmt.Errorf("check mutation tags: %w", err)
	}
	if tagCount != len(params.TagIDs) {
		return TagMutationResult{}, ErrNotFound
	}
	var changed int
	action := "asset.tags_added"
	if add {
		if err := tx.QueryRow(ctx, `
			WITH inserted AS (
				INSERT INTO asset_tags (workspace_id, asset_id, tag_id, created_by)
				SELECT $1, $2, requested.tag_id, $4
				FROM unnest($3::uuid[]) AS requested(tag_id)
				ON CONFLICT DO NOTHING
				RETURNING 1
			)
			SELECT count(*) FROM inserted`,
			params.WorkspaceID, params.AssetID, params.TagIDs, params.ActorID,
		).Scan(&changed); err != nil {
			return TagMutationResult{}, fmt.Errorf("add asset tags: %w", err)
		}
	} else {
		action = "asset.tags_removed"
		if err := tx.QueryRow(ctx, `
			WITH removed AS (
				DELETE FROM asset_tags
				WHERE workspace_id = $1 AND asset_id = $2 AND tag_id = ANY($3::uuid[])
				RETURNING 1
			)
			SELECT count(*) FROM removed`,
			params.WorkspaceID, params.AssetID, params.TagIDs,
		).Scan(&changed); err != nil {
			return TagMutationResult{}, fmt.Errorf("remove asset tags: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, $4, $5, 'asset', $6, $7,
			jsonb_build_object(
				'tag_ids', $8::uuid[], 'changed_count', $9::integer,
				'api_key_id', NULLIF($10::text, '')
			)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.ActorType,
		action, params.AssetID, params.RequestID, params.TagIDs, changed, params.CredentialID,
	); err != nil {
		return TagMutationResult{}, fmt.Errorf("insert tag mutation audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return TagMutationResult{}, fmt.Errorf("commit tag mutation: %w", err)
	}
	return TagMutationResult{AssetID: params.AssetID, TagIDs: params.TagIDs, ChangedCount: changed}, nil
}

func (repository *PostgresRepository) CreateAnnotation(
	ctx context.Context,
	params AnnotationCreateParams,
) (Annotation, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Annotation{}, fmt.Errorf("begin annotation creation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var assetExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM assets
			WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		)`, params.WorkspaceID, params.AssetID).Scan(&assetExists); err != nil {
		return Annotation{}, fmt.Errorf("check annotation asset: %w", err)
	}
	if !assetExists {
		return Annotation{}, ErrNotFound
	}
	created, err := scanAnnotation(tx.QueryRow(ctx, `
		INSERT INTO annotations (
			id, workspace_id, asset_id, kind, start_ms, end_ms, body, created_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text, workspace_id::text, asset_id::text, kind, start_ms,
		          end_ms, body, version, created_by::text, created_at, updated_at`,
		params.ID, params.WorkspaceID, params.AssetID, params.Kind, params.StartMS,
		params.EndMS, params.Body, params.ActorID,
	))
	if err != nil {
		return Annotation{}, fmt.Errorf("insert annotation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, $4, 'annotation.created', 'annotation', $5, $6,
			jsonb_build_object(
				'asset_id', $7::uuid, 'kind', $8::text, 'start_ms', $9::bigint,
				'end_ms', $10::bigint, 'api_key_id', NULLIF($11::text, '')
			)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.ActorType,
		params.ID, params.RequestID, params.AssetID, params.Kind, params.StartMS,
		params.EndMS, params.CredentialID,
	); err != nil {
		return Annotation{}, fmt.Errorf("insert annotation audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Annotation{}, fmt.Errorf("commit annotation creation: %w", err)
	}
	return created, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func scanAnnotation(row interface{ Scan(...any) error }) (Annotation, error) {
	var result Annotation
	err := row.Scan(
		&result.ID, &result.WorkspaceID, &result.AssetID, &result.Kind,
		&result.StartMS, &result.EndMS, &result.Body, &result.Version,
		&result.CreatedBy, &result.CreatedAt, &result.UpdatedAt,
	)
	return result, err
}
