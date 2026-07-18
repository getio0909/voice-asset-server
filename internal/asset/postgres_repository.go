package asset

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

func (r *PostgresRepository) Create(ctx context.Context, params CreateParams) (Asset, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Asset{}, false, fmt.Errorf("begin asset transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	created, err := scanAsset(tx.QueryRow(ctx, `
		INSERT INTO assets (
			id, workspace_id, title, language, status, created_by,
			idempotency_key, idempotency_request_hash
		) VALUES ($1, $2, $3, $4, 'draft', $5, $6, $7)
		ON CONFLICT (workspace_id, idempotency_key)
			WHERE idempotency_key IS NOT NULL
			DO NOTHING
		RETURNING id::text, workspace_id::text, collection_id::text, title, language, status,
		          duration_ms, version, created_at, updated_at`,
		params.AssetID,
		params.WorkspaceID,
		params.Title,
		params.Language,
		params.CreatedBy,
		params.IdempotencyKey,
		params.RequestHash,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		var requestHash string
		existing, queryErr := scanAssetWithHash(tx.QueryRow(ctx, `
			SELECT id::text, workspace_id::text, collection_id::text, title, language, status,
			       duration_ms, version, created_at, updated_at, idempotency_request_hash
			FROM assets
			WHERE workspace_id = $1 AND idempotency_key = $2`,
			params.WorkspaceID, params.IdempotencyKey,
		), &requestHash)
		if queryErr != nil {
			return Asset{}, false, fmt.Errorf("load idempotent asset: %w", queryErr)
		}
		if requestHash != params.RequestHash {
			return Asset{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}
	if err != nil {
		return Asset{}, false, fmt.Errorf("insert asset: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type, target_id, metadata
		) VALUES ($1, $2, $3, 'user', 'asset.created', 'asset', $4, '{}')`,
		params.AuditID, params.WorkspaceID, params.CreatedBy, params.AssetID,
	); err != nil {
		return Asset{}, false, fmt.Errorf("insert asset audit log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Asset{}, false, fmt.Errorf("commit asset transaction: %w", err)
	}
	return created, false, nil
}

func (r *PostgresRepository) Get(ctx context.Context, workspaceID, assetID string) (Asset, error) {
	result, err := scanAsset(r.pool.QueryRow(ctx, `
		SELECT id::text, workspace_id::text, collection_id::text, title, language, status,
		       duration_ms, version, created_at, updated_at
		FROM assets
		WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`, assetID, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("query asset: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) List(ctx context.Context, params ListParams) ([]Asset, error) {
	var beforeID any
	if params.BeforeCreatedAt != nil {
		beforeID = params.BeforeID
	}
	rows, err := r.pool.Query(ctx, `
		WITH requested_search AS (
			SELECT CASE
				WHEN $2::text = '' THEN NULL::tsquery
				ELSE websearch_to_tsquery('simple'::regconfig, $2::text)
			END AS terms
		)
		SELECT id::text, workspace_id::text, collection_id::text, title, language, status,
		       duration_ms, version, created_at, updated_at,
		       ($2::text <> '' AND (
		         assets.search_vector @@ requested_search.terms
		         OR strpos(lower(title), lower($2::text)) > 0
		       )) AS title_match
		FROM assets
		CROSS JOIN requested_search
		WHERE workspace_id = $1
		  AND (
		    $2::text = ''
		    OR assets.search_vector @@ requested_search.terms
		    OR strpos(lower(title), lower($2::text)) > 0
		    OR EXISTS (
		      SELECT 1
		      FROM transcripts
		      JOIN LATERAL (
		        SELECT revision.id
		        FROM transcript_revisions revision
		        WHERE revision.transcript_id = transcripts.id
		        ORDER BY revision.created_at DESC, revision.id DESC
		        LIMIT 1
		      ) latest_revision ON true
		      JOIN transcript_segments segment ON segment.revision_id = latest_revision.id
		      WHERE transcripts.asset_id = assets.id
		        AND ($12::text = '' OR lower(segment.speaker) = lower($12::text))
		        AND (
		          segment.search_vector @@ requested_search.terms
		          OR strpos(lower(segment.text_content), lower($2::text)) > 0
		        )
		    )
		  )
		  AND ($3::timestamptz IS NULL OR (created_at, id) < ($3::timestamptz, $4::uuid))
		  AND (
		    ($6 = '' AND deleted_at IS NULL)
		    OR ($6 = 'trashed' AND deleted_at IS NOT NULL AND status = 'trashed')
		    OR ($6 NOT IN ('', 'trashed') AND deleted_at IS NULL AND status = $6)
		  )
		  AND ($7 = '' OR collection_id = NULLIF($7, '')::uuid)
		  AND ($8 = '' OR EXISTS (
		    SELECT 1 FROM asset_tags
		    WHERE asset_tags.workspace_id = assets.workspace_id
		      AND asset_tags.asset_id = assets.id
		      AND asset_tags.tag_id = NULLIF($8, '')::uuid
		  ))
		  AND ($9::timestamptz IS NULL OR created_at >= $9::timestamptz)
		  AND ($10::timestamptz IS NULL OR created_at < $10::timestamptz)
		  AND ($11::text = '' OR EXISTS (
		    SELECT 1
		    FROM transcripts
		    JOIN transcript_revisions revision ON revision.transcript_id = transcripts.id
		    WHERE transcripts.asset_id = assets.id
		      AND revision.kind = 'raw_asr'
		      AND revision.provider_snapshot ->> 'provider_id' = $11::text
		  ))
		  AND ($12::text = '' OR EXISTS (
		    SELECT 1
		    FROM transcripts
		    JOIN LATERAL (
		      SELECT revision.id
		      FROM transcript_revisions revision
		      WHERE revision.transcript_id = transcripts.id
		      ORDER BY revision.created_at DESC, revision.id DESC
		      LIMIT 1
		    ) latest_revision ON true
		    JOIN transcript_segments segment ON segment.revision_id = latest_revision.id
		    WHERE transcripts.asset_id = assets.id
		      AND lower(segment.speaker) = lower($12::text)
		  ))
		ORDER BY created_at DESC, id DESC
		LIMIT $5`,
		params.WorkspaceID,
		params.Query,
		params.BeforeCreatedAt,
		beforeID,
		params.Limit,
		params.Status,
		params.CollectionID,
		params.TagID,
		params.CreatedFrom,
		params.CreatedBefore,
		params.ProviderID,
		params.Speaker,
	)
	if err != nil {
		return nil, fmt.Errorf("query assets: %w", err)
	}
	defer rows.Close()

	searchRequested := params.Query != "" || params.ProviderID != "" || params.Speaker != ""
	results := make([]Asset, 0, params.Limit)
	for rows.Next() {
		result, titleMatch, scanErr := scanAssetWithTitleMatch(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan asset: %w", scanErr)
		}
		if searchRequested {
			result.Search = &SearchMatch{
				Title: titleMatch, ProviderIDs: make([]string, 0), Segments: make([]SegmentHit, 0),
			}
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assets: %w", err)
	}
	rows.Close()
	if searchRequested && len(results) > 0 {
		if err := r.loadSearchMatches(ctx, results, params); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (r *PostgresRepository) loadSearchMatches(ctx context.Context, results []Asset, params ListParams) error {
	assetIDs := make([]string, 0, len(results))
	assetIndexes := make(map[string]int, len(results))
	for index := range results {
		assetIDs = append(assetIDs, results[index].ID)
		assetIndexes[results[index].ID] = index
	}

	providerRows, err := r.pool.Query(ctx, `
		SELECT DISTINCT transcripts.asset_id::text,
		       revision.provider_snapshot ->> 'provider_id' AS provider_id
		FROM transcripts
		JOIN transcript_revisions revision ON revision.transcript_id = transcripts.id
		WHERE transcripts.asset_id = ANY($1::uuid[])
		  AND revision.kind = 'raw_asr'
		  AND revision.provider_snapshot ->> 'provider_id' <> ''
		ORDER BY transcripts.asset_id::text, provider_id`, assetIDs)
	if err != nil {
		return fmt.Errorf("query asset search providers: %w", err)
	}
	for providerRows.Next() {
		var assetID, providerID string
		if err := providerRows.Scan(&assetID, &providerID); err != nil {
			providerRows.Close()
			return fmt.Errorf("scan asset search provider: %w", err)
		}
		if index, found := assetIndexes[assetID]; found {
			results[index].Search.ProviderIDs = append(results[index].Search.ProviderIDs, providerID)
		}
	}
	if err := providerRows.Err(); err != nil {
		providerRows.Close()
		return fmt.Errorf("iterate asset search providers: %w", err)
	}
	providerRows.Close()

	if params.Query == "" && params.Speaker == "" {
		return nil
	}
	hitRows, err := r.pool.Query(ctx, `
		WITH requested_search AS (
			SELECT CASE
				WHEN $2::text = '' THEN NULL::tsquery
				ELSE websearch_to_tsquery('simple'::regconfig, $2::text)
			END AS terms
		), latest_revisions AS (
			SELECT transcripts.asset_id, transcripts.id AS transcript_id, latest_revision.id AS revision_id
			FROM transcripts
			JOIN LATERAL (
				SELECT revision.id
				FROM transcript_revisions revision
				WHERE revision.transcript_id = transcripts.id
				ORDER BY revision.created_at DESC, revision.id DESC
				LIMIT 1
			) latest_revision ON true
			WHERE transcripts.asset_id = ANY($1::uuid[])
		), ranked_hits AS (
			SELECT latest_revisions.asset_id, latest_revisions.transcript_id,
			       latest_revisions.revision_id, segment.id AS segment_id,
			       segment.ordinal, segment.start_ms, segment.end_ms, segment.speaker,
			       left(segment.text_content, 1000) AS text_excerpt,
			       row_number() OVER (
			         PARTITION BY latest_revisions.asset_id
			         ORDER BY segment.start_ms, segment.end_ms, segment.ordinal, segment.id
			       ) AS hit_number
			FROM latest_revisions
			JOIN transcript_segments segment ON segment.revision_id = latest_revisions.revision_id
			CROSS JOIN requested_search
			WHERE ($2::text = '' OR (
			        segment.search_vector @@ requested_search.terms
			        OR strpos(lower(segment.text_content), lower($2::text)) > 0
			      ))
			  AND ($3::text = '' OR lower(segment.speaker) = lower($3::text))
		)
		SELECT asset_id::text, transcript_id::text, revision_id::text, segment_id::text,
		       ordinal, start_ms, end_ms, speaker, text_excerpt
		FROM ranked_hits
		WHERE hit_number <= 5
		ORDER BY asset_id, hit_number`, assetIDs, params.Query, params.Speaker)
	if err != nil {
		return fmt.Errorf("query asset search segment hits: %w", err)
	}
	defer hitRows.Close()
	for hitRows.Next() {
		var assetID string
		var hit SegmentHit
		if err := hitRows.Scan(
			&assetID, &hit.TranscriptID, &hit.RevisionID, &hit.SegmentID,
			&hit.Ordinal, &hit.StartMS, &hit.EndMS, &hit.Speaker, &hit.Text,
		); err != nil {
			return fmt.Errorf("scan asset search segment hit: %w", err)
		}
		if index, found := assetIndexes[assetID]; found {
			results[index].Search.Segments = append(results[index].Search.Segments, hit)
		}
	}
	if err := hitRows.Err(); err != nil {
		return fmt.Errorf("iterate asset search segment hits: %w", err)
	}
	return nil
}

func (r *PostgresRepository) UpdateMetadata(
	ctx context.Context,
	params UpdateMetadataParams,
) (Asset, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Asset{}, fmt.Errorf("begin asset metadata update: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if params.CollectionID != nil {
		var collectionExists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM collections WHERE workspace_id = $1 AND id = $2
			)`, params.WorkspaceID, *params.CollectionID,
		).Scan(&collectionExists); err != nil {
			return Asset{}, fmt.Errorf("check asset collection: %w", err)
		}
		if !collectionExists {
			return Asset{}, ErrNotFound
		}
	}

	updated, err := scanAsset(tx.QueryRow(ctx, `
		UPDATE assets
		SET title = $3, language = $4, collection_id = $5,
		    version = version + 1, updated_at = clock_timestamp()
		WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL AND version = $6
		RETURNING id::text, workspace_id::text, collection_id::text, title, language,
		          status, duration_ms, version, created_at, updated_at`,
		params.AssetID, params.WorkspaceID, params.Title, params.Language,
		params.CollectionID, params.ExpectedVersion,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		var currentVersion int64
		if lookupErr := tx.QueryRow(ctx, `
			SELECT version FROM assets
			WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL`,
			params.AssetID, params.WorkspaceID,
		).Scan(&currentVersion); errors.Is(lookupErr, pgx.ErrNoRows) {
			return Asset{}, ErrNotFound
		} else if lookupErr != nil {
			return Asset{}, fmt.Errorf("resolve asset metadata conflict: %w", lookupErr)
		}
		return Asset{}, ErrConflict
	}
	if err != nil {
		return Asset{}, fmt.Errorf("update asset metadata: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, $4, 'asset.metadata_updated', 'asset', $5, $6,
			jsonb_build_object(
				'title', $7::text, 'language', $8::text,
				'collection_id', $9::uuid, 'version', $10::bigint,
				'api_key_id', NULLIF($11::text, '')
			)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.ActorType,
		params.AssetID, params.RequestID, params.Title, params.Language,
		params.CollectionID, updated.Version, params.CredentialID,
	); err != nil {
		return Asset{}, fmt.Errorf("insert asset metadata audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Asset{}, fmt.Errorf("commit asset metadata update: %w", err)
	}
	return updated, nil
}

func (r *PostgresRepository) Trash(ctx context.Context, params LifecycleParams) (Asset, error) {
	return r.changeLifecycle(ctx, params, true)
}

func (r *PostgresRepository) Restore(ctx context.Context, params LifecycleParams) (Asset, error) {
	return r.changeLifecycle(ctx, params, false)
}

func (r *PostgresRepository) RequestPurge(
	ctx context.Context,
	params PurgeParams,
) (PurgeRequest, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return PurgeRequest{}, false, fmt.Errorf("begin asset purge request: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if existing, requestHash, loadErr := loadPurgeByIdempotency(
		ctx, tx, params.WorkspaceID, params.IdempotencyKey, params.AssetID,
	); loadErr == nil {
		if requestHash != params.RequestHash {
			return PurgeRequest{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	} else if errors.Is(loadErr, ErrIdempotencyConflict) {
		return PurgeRequest{}, false, ErrIdempotencyConflict
	} else if !errors.Is(loadErr, pgx.ErrNoRows) {
		return PurgeRequest{}, false, fmt.Errorf("load idempotent asset purge: %w", loadErr)
	}

	var status string
	var deletedAt any
	var version int64
	err = tx.QueryRow(ctx, `
		SELECT status, deleted_at, version
		FROM assets
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE`, params.AssetID, params.WorkspaceID,
	).Scan(&status, &deletedAt, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		// The worker may have completed between the first idempotency lookup
		// and this lock attempt. Preserve replay semantics after asset removal.
		existing, requestHash, loadErr := loadPurgeByIdempotency(
			ctx, tx, params.WorkspaceID, params.IdempotencyKey, params.AssetID,
		)
		if loadErr == nil {
			if requestHash != params.RequestHash {
				return PurgeRequest{}, false, ErrIdempotencyConflict
			}
			return existing, true, nil
		}
		if errors.Is(loadErr, ErrIdempotencyConflict) {
			return PurgeRequest{}, false, ErrIdempotencyConflict
		}
		if !errors.Is(loadErr, pgx.ErrNoRows) {
			return PurgeRequest{}, false, fmt.Errorf("reload completed asset purge: %w", loadErr)
		}
		return PurgeRequest{}, false, ErrNotFound
	}
	if err != nil {
		return PurgeRequest{}, false, fmt.Errorf("lock asset for purge: %w", err)
	}

	// A same-key request can commit while this transaction waits for the asset
	// lock. Re-check before interpreting the internal purging state.
	if existing, requestHash, loadErr := loadPurgeByIdempotency(
		ctx, tx, params.WorkspaceID, params.IdempotencyKey, params.AssetID,
	); loadErr == nil {
		if requestHash != params.RequestHash {
			return PurgeRequest{}, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	} else if errors.Is(loadErr, ErrIdempotencyConflict) {
		return PurgeRequest{}, false, ErrIdempotencyConflict
	} else if !errors.Is(loadErr, pgx.ErrNoRows) {
		return PurgeRequest{}, false, fmt.Errorf("reload idempotent asset purge: %w", loadErr)
	}

	if version != params.ExpectedVersion {
		return PurgeRequest{}, false, ErrConflict
	}
	if (status != "trashed" && status != "purging") || deletedAt == nil {
		return PurgeRequest{}, false, ErrPurgeNotEligible
	}
	resuming := status == "purging"
	var activeJobs int
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM jobs
		WHERE asset_id = $1 AND state IN ('queued', 'running', 'retry_wait')`,
		params.AssetID,
	).Scan(&activeJobs); err != nil {
		return PurgeRequest{}, false, fmt.Errorf("check active asset jobs: %w", err)
	}
	if activeJobs != 0 {
		return PurgeRequest{}, false, ErrPurgeNotEligible
	}
	reportedVersion := params.ExpectedVersion
	if !resuming {
		reportedVersion++
	}

	requested, err := scanPurgeRequest(tx.QueryRow(ctx, `
		INSERT INTO jobs (
			id, workspace_id, asset_id, created_by, kind, state, payload,
			max_attempts, idempotency_key, idempotency_request_hash
		) VALUES (
			$1, $2, $3, $4, 'purge_asset', 'queued',
			jsonb_build_object(
				'asset_id', $3::uuid::text,
				'requested_version', $5::bigint,
				'asset_version', $8::bigint
			),
			10, $6, $7
		)
		ON CONFLICT (workspace_id, idempotency_key)
			WHERE idempotency_key IS NOT NULL
			DO NOTHING
		RETURNING id::text, asset_id::text, $8::bigint, state, created_at`,
		params.JobID, params.WorkspaceID, params.AssetID, params.ActorID,
		params.ExpectedVersion, params.IdempotencyKey, params.RequestHash, reportedVersion,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, requestHash, loadErr := loadPurgeByIdempotency(
			ctx, tx, params.WorkspaceID, params.IdempotencyKey, params.AssetID,
		)
		if errors.Is(loadErr, ErrIdempotencyConflict) || (loadErr == nil && requestHash != params.RequestHash) {
			return PurgeRequest{}, false, ErrIdempotencyConflict
		}
		if loadErr != nil {
			return PurgeRequest{}, false, fmt.Errorf("load concurrent asset purge: %w", loadErr)
		}
		return existing, true, nil
	}
	if err != nil {
		return PurgeRequest{}, false, fmt.Errorf("insert asset purge job: %w", err)
	}
	if !resuming {
		command, err := tx.Exec(ctx, `
			UPDATE assets
			SET status = 'purging', version = version + 1, updated_at = clock_timestamp()
			WHERE id = $1 AND workspace_id = $2 AND status = 'trashed'
			  AND deleted_at IS NOT NULL AND version = $3`,
			params.AssetID, params.WorkspaceID, params.ExpectedVersion,
		)
		if err != nil {
			return PurgeRequest{}, false, fmt.Errorf("mark asset purging: %w", err)
		}
		if command.RowsAffected() != 1 {
			return PurgeRequest{}, false, ErrConflict
		}
	}
	action := "asset.purge_requested"
	auditVersion := params.ExpectedVersion + 1
	if resuming {
		action = "asset.purge_resumed"
		auditVersion = params.ExpectedVersion
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, $4, $10, 'asset', $5, $6,
			jsonb_build_object(
				'job_id', $7::uuid::text, 'version', $8::bigint,
				'api_key_id', NULLIF($9::text, '')
			)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.ActorType,
		params.AssetID, params.RequestID, params.JobID, auditVersion,
		params.CredentialID, action,
	); err != nil {
		return PurgeRequest{}, false, fmt.Errorf("insert asset purge audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PurgeRequest{}, false, fmt.Errorf("commit asset purge request: %w", err)
	}
	return requested, false, nil
}

func loadPurgeByIdempotency(
	ctx context.Context,
	tx pgx.Tx,
	workspaceID,
	idempotencyKey,
	assetID string,
) (PurgeRequest, string, error) {
	var requestHash string
	var kind string
	result, err := scanPurgeRequestWithKindAndHash(tx.QueryRow(ctx, `
		SELECT id::text, COALESCE(asset_id::text, $3::text),
		       COALESCE(
		         (SELECT version FROM assets WHERE id = COALESCE(jobs.asset_id, $3::uuid)),
		         (payload->>'asset_version')::bigint,
		         (payload->>'requested_version')::bigint + 1
		       ), state, created_at,
		       kind, idempotency_request_hash
		FROM jobs
		WHERE workspace_id = $1 AND idempotency_key = $2`,
		workspaceID, idempotencyKey, assetID,
	), &kind, &requestHash)
	if err == nil && kind != "purge_asset" {
		return PurgeRequest{}, "", ErrIdempotencyConflict
	}
	return result, requestHash, err
}

func (r *PostgresRepository) GetPurge(
	ctx context.Context,
	workspaceID,
	jobID string,
) (PurgeRequest, error) {
	result, err := scanPurgeRequest(r.pool.QueryRow(ctx, `
		SELECT id::text, COALESCE(asset_id::text, payload->>'asset_id'),
		       COALESCE(
		         (SELECT version FROM assets WHERE id = jobs.asset_id),
		         (payload->>'asset_version')::bigint,
		         (payload->>'requested_version')::bigint + 1
		       ), state, created_at
		FROM jobs
		WHERE id = $1 AND workspace_id = $2 AND kind = 'purge_asset'`,
		jobID, workspaceID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return PurgeRequest{}, ErrNotFound
	}
	if err != nil {
		return PurgeRequest{}, fmt.Errorf("query asset purge job: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) changeLifecycle(
	ctx context.Context,
	params LifecycleParams,
	trash bool,
) (Asset, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Asset{}, fmt.Errorf("begin asset lifecycle change: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var lifecycleStatus string
	var updated Asset
	if trash {
		updated, err = scanAssetWithLifecycleStatus(tx.QueryRow(ctx, `
			UPDATE assets
			SET status_before_trash = status, status = 'trashed',
			    deleted_at = clock_timestamp(), version = version + 1,
			    updated_at = clock_timestamp()
			WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL AND version = $3
			RETURNING id::text, workspace_id::text, collection_id::text, title, language,
			          status, duration_ms, version, created_at, updated_at, status_before_trash`,
			params.AssetID, params.WorkspaceID, params.ExpectedVersion,
		), &lifecycleStatus)
	} else {
		updated, err = scanAssetWithLifecycleStatus(tx.QueryRow(ctx, `
			UPDATE assets
			SET status = COALESCE(status_before_trash, 'draft'), status_before_trash = NULL,
			    deleted_at = NULL, version = version + 1, updated_at = clock_timestamp()
			WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NOT NULL
			  AND status = 'trashed' AND version = $3
			RETURNING id::text, workspace_id::text, collection_id::text, title, language,
			          status, duration_ms, version, created_at, updated_at, status`,
			params.AssetID, params.WorkspaceID, params.ExpectedVersion,
		), &lifecycleStatus)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var currentVersion int64
		if lookupErr := tx.QueryRow(ctx, `
			SELECT version FROM assets WHERE id = $1 AND workspace_id = $2`,
			params.AssetID, params.WorkspaceID,
		).Scan(&currentVersion); errors.Is(lookupErr, pgx.ErrNoRows) {
			return Asset{}, ErrNotFound
		} else if lookupErr != nil {
			return Asset{}, fmt.Errorf("resolve asset lifecycle conflict: %w", lookupErr)
		}
		return Asset{}, ErrConflict
	}
	if err != nil {
		return Asset{}, fmt.Errorf("update asset lifecycle: %w", err)
	}

	action := "asset.restored"
	if trash {
		action = "asset.trashed"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, request_id, metadata
		) VALUES (
			$1, $2, $3, $4, $5, 'asset', $6, $7,
			jsonb_build_object(
				'status', $8::text, 'version', $9::bigint,
				'api_key_id', NULLIF($10::text, '')
			)
		)`, params.AuditID, params.WorkspaceID, params.ActorID, params.ActorType,
		action, params.AssetID, params.RequestID, lifecycleStatus, updated.Version,
		params.CredentialID,
	); err != nil {
		return Asset{}, fmt.Errorf("insert asset lifecycle audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Asset{}, fmt.Errorf("commit asset lifecycle change: %w", err)
	}
	return updated, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPurgeRequest(row rowScanner) (PurgeRequest, error) {
	var result PurgeRequest
	err := row.Scan(&result.JobID, &result.AssetID, &result.AssetVersion, &result.State, &result.RequestedAt)
	return result, err
}

func scanPurgeRequestWithKindAndHash(
	row rowScanner,
	kind,
	requestHash *string,
) (PurgeRequest, error) {
	var result PurgeRequest
	err := row.Scan(
		&result.JobID, &result.AssetID, &result.AssetVersion,
		&result.State, &result.RequestedAt, kind, requestHash,
	)
	return result, err
}

func scanAsset(row rowScanner) (Asset, error) {
	var result Asset
	err := row.Scan(
		&result.ID,
		&result.WorkspaceID,
		&result.CollectionID,
		&result.Title,
		&result.Language,
		&result.Status,
		&result.DurationMS,
		&result.Version,
		&result.CreatedAt,
		&result.UpdatedAt,
	)
	return result, err
}

func scanAssetWithTitleMatch(row rowScanner) (Asset, bool, error) {
	var result Asset
	var titleMatch bool
	err := row.Scan(
		&result.ID,
		&result.WorkspaceID,
		&result.CollectionID,
		&result.Title,
		&result.Language,
		&result.Status,
		&result.DurationMS,
		&result.Version,
		&result.CreatedAt,
		&result.UpdatedAt,
		&titleMatch,
	)
	return result, titleMatch, err
}

func scanAssetWithHash(row rowScanner, requestHash *string) (Asset, error) {
	var result Asset
	err := row.Scan(
		&result.ID,
		&result.WorkspaceID,
		&result.CollectionID,
		&result.Title,
		&result.Language,
		&result.Status,
		&result.DurationMS,
		&result.Version,
		&result.CreatedAt,
		&result.UpdatedAt,
		requestHash,
	)
	return result, err
}

func scanAssetWithLifecycleStatus(row rowScanner, lifecycleStatus *string) (Asset, error) {
	var result Asset
	err := row.Scan(
		&result.ID,
		&result.WorkspaceID,
		&result.CollectionID,
		&result.Title,
		&result.Language,
		&result.Status,
		&result.DurationMS,
		&result.Version,
		&result.CreatedAt,
		&result.UpdatedAt,
		lifecycleStatus,
	)
	return result, err
}
