package transcript

import (
	"context"
	"encoding/json"
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

func (r *PostgresRepository) List(ctx context.Context, workspaceID, assetID string) ([]Summary, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM assets
			WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
		)`, assetID, workspaceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check transcript asset: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `
		SELECT t.id::text, t.asset_id::text, t.language,
		       latest.id::text, latest.kind, latest.text_content,
		       t.created_at, latest.created_at
		FROM transcripts t
		JOIN assets a ON a.id = t.asset_id
		JOIN LATERAL (
			SELECT id, kind, text_content, created_at
			FROM transcript_revisions
			WHERE transcript_id = t.id
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		) latest ON true
		WHERE t.asset_id = $1 AND a.workspace_id = $2 AND a.deleted_at IS NULL
		ORDER BY t.created_at DESC, t.id DESC`, assetID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query transcript summaries: %w", err)
	}
	defer rows.Close()
	results := make([]Summary, 0)
	for rows.Next() {
		var result Summary
		if err := rows.Scan(
			&result.ID,
			&result.AssetID,
			&result.Language,
			&result.LatestRevisionID,
			&result.LatestKind,
			&result.LatestText,
			&result.CreatedAt,
			&result.RevisionCreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan transcript summary: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transcript summaries: %w", err)
	}
	return results, nil
}

func (r *PostgresRepository) GetRevision(
	ctx context.Context,
	workspaceID,
	revisionID string,
) (Revision, error) {
	var (
		result           Revision
		providerSnapshot []byte
		hotwordSnapshot  []byte
		glossarySnapshot []byte
		diff             []byte
		validationResult []byte
		parentRevisionID *string
		rawObjectID      *string
		sourceJobID      *string
		createdBy        *string
		model            *string
		promptVersion    *string
	)
	err := r.pool.QueryRow(ctx, `
		SELECT revision.id::text, revision.transcript_id::text,
		       transcript.asset_id::text, revision.parent_revision_id::text,
		       revision.kind, transcript.language, revision.text_content,
		       revision.provider_snapshot, revision.hotword_snapshot,
		       revision.glossary_snapshot, revision.diff, revision.validation_result,
		       revision.provider_raw_object_id::text, revision.source_job_id::text,
		       revision.created_by::text, revision.created_by_type,
		       revision.model, revision.prompt_version, revision.review_status,
		       revision.created_at
		FROM transcript_revisions revision
		JOIN transcripts transcript ON transcript.id = revision.transcript_id
		JOIN assets asset ON asset.id = transcript.asset_id
		WHERE revision.id = $1 AND asset.workspace_id = $2 AND asset.deleted_at IS NULL`,
		revisionID, workspaceID,
	).Scan(
		&result.ID,
		&result.TranscriptID,
		&result.AssetID,
		&parentRevisionID,
		&result.Kind,
		&result.Language,
		&result.Text,
		&providerSnapshot,
		&hotwordSnapshot,
		&glossarySnapshot,
		&diff,
		&validationResult,
		&rawObjectID,
		&sourceJobID,
		&createdBy,
		&result.CreatedByType,
		&model,
		&promptVersion,
		&result.ReviewStatus,
		&result.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	if err != nil {
		return Revision{}, fmt.Errorf("query transcript revision: %w", err)
	}
	result.ProviderSnapshot = append(json.RawMessage(nil), providerSnapshot...)
	result.HotwordSnapshot = append(json.RawMessage(nil), hotwordSnapshot...)
	result.GlossarySnapshot = append(json.RawMessage(nil), glossarySnapshot...)
	result.Diff = append(json.RawMessage(nil), diff...)
	result.ValidationResult = append(json.RawMessage(nil), validationResult...)
	if parentRevisionID != nil {
		result.ParentRevisionID = *parentRevisionID
	}
	if rawObjectID != nil {
		result.ProviderRawObjectID = *rawObjectID
	}
	if sourceJobID != nil {
		result.SourceJobID = *sourceJobID
	}
	if createdBy != nil {
		result.CreatedBy = *createdBy
	}
	if model != nil {
		result.Model = *model
	}
	if promptVersion != nil {
		result.PromptVersion = *promptVersion
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id::text, ordinal, start_ms, end_ms, speaker,
		       text_content, confidence, words
		FROM transcript_segments
		WHERE revision_id = $1
		ORDER BY ordinal`, revisionID)
	if err != nil {
		return Revision{}, fmt.Errorf("query transcript segments: %w", err)
	}
	defer rows.Close()
	result.Segments = make([]Segment, 0)
	for rows.Next() {
		var (
			segment Segment
			words   []byte
		)
		if err := rows.Scan(
			&segment.ID,
			&segment.Ordinal,
			&segment.StartMS,
			&segment.EndMS,
			&segment.Speaker,
			&segment.Text,
			&segment.Confidence,
			&words,
		); err != nil {
			return Revision{}, fmt.Errorf("scan transcript segment: %w", err)
		}
		segment.Words = append(json.RawMessage(nil), words...)
		result.Segments = append(result.Segments, segment)
	}
	if err := rows.Err(); err != nil {
		return Revision{}, fmt.Errorf("iterate transcript segments: %w", err)
	}
	return result, nil
}
