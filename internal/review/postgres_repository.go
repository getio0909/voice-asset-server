package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/transcript"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) AddDecision(ctx context.Context, params DecisionParams) (Record, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return Record{}, fmt.Errorf("begin review decision: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	target, err := loadRevision(ctx, tx, params.WorkspaceID, params.RevisionID)
	if err != nil {
		return Record{}, err
	}
	changes, err := decodeChanges(target.Diff)
	if err != nil || target.Kind != transcript.KindLLMCorrected {
		return Record{}, ErrConflict
	}
	if params.ChangeIndex != nil && *params.ChangeIndex >= len(changes) {
		return Record{}, ErrInvalidInput
	}
	var createdAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO transcript_revision_reviews (
			id, workspace_id, revision_id, reviewer_id, action, change_index
		) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`, params.ID, params.WorkspaceID, params.RevisionID,
		params.ReviewerID, params.Action, params.ChangeIndex).Scan(&createdAt)
	if err != nil {
		return Record{}, fmt.Errorf("insert review decision: %w", err)
	}
	metadata, err := json.Marshal(struct {
		Action      string `json:"action"`
		ChangeIndex *int   `json:"change_index,omitempty"`
	}{params.Action, params.ChangeIndex})
	if err != nil {
		return Record{}, fmt.Errorf("encode review audit: %w", err)
	}
	if err := insertAudit(ctx, tx, params.AuditID, params.WorkspaceID, params.ReviewerID,
		"correction.reviewed", params.RevisionID, metadata, createdAt); err != nil {
		return Record{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Record{}, fmt.Errorf("commit review decision: %w", err)
	}
	return Record{
		ID: params.ID, RevisionID: params.RevisionID, ReviewerID: params.ReviewerID,
		Action: params.Action, ChangeIndex: cloneInt(params.ChangeIndex), CreatedAt: createdAt,
	}, nil
}

func (repository *PostgresRepository) Approve(ctx context.Context, params ApprovalParams) (ApprovalResult, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return ApprovalResult{}, fmt.Errorf("begin approval: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return ApprovalResult{}, fmt.Errorf("resolve approval clock: %w", err)
	}
	source, err := loadRevision(ctx, tx, params.WorkspaceID, params.RevisionID)
	if err != nil {
		return ApprovalResult{}, err
	}
	if source.Kind != transcript.KindLLMCorrected || source.ParentRevisionID == "" ||
		source.ReviewStatus != "pending" {
		return ApprovalResult{}, ErrConflict
	}
	parent, err := loadRevision(ctx, tx, params.WorkspaceID, source.ParentRevisionID)
	if err != nil {
		return ApprovalResult{}, ErrConflict
	}
	changes, err := decodeChanges(source.Diff)
	if err != nil || len(source.Segments) != len(parent.Segments) ||
		len(params.HumanSegmentIDs) != len(parent.Segments) ||
		len(params.ApprovedSegmentIDs) != len(parent.Segments) {
		return ApprovalResult{}, ErrConflict
	}
	decisions, err := loadDecisions(ctx, tx, params.WorkspaceID, source.ID)
	if err != nil {
		return ApprovalResult{}, err
	}
	accepted := make([]bool, len(changes))
	for index := range accepted {
		accepted[index] = params.AcceptPending
	}
	for _, decision := range decisions {
		switch decision.Action {
		case ActionAcceptAll:
			for index := range accepted {
				accepted[index] = true
			}
		case ActionRejectAll:
			for index := range accepted {
				accepted[index] = false
			}
		case ActionAcceptChange, ActionRejectChange:
			if decision.ChangeIndex == nil || *decision.ChangeIndex >= len(accepted) {
				return ApprovalResult{}, ErrConflict
			}
			accepted[*decision.ChangeIndex] = decision.Action == ActionAcceptChange
		}
	}
	humanText, humanSegments, acceptedIndexes, err := buildReviewedContent(
		parent, source, changes, accepted, params.HumanSegmentIDs,
	)
	if err != nil {
		return ApprovalResult{}, ErrConflict
	}
	approvedSegments := cloneWithIDs(humanSegments, params.ApprovedSegmentIDs)
	reviewDiff, err := json.Marshal(struct {
		SourceRevisionID      string `json:"source_revision_id"`
		AcceptedChangeIndexes []int  `json:"accepted_change_indexes"`
	}{source.ID, acceptedIndexes})
	if err != nil {
		return ApprovalResult{}, fmt.Errorf("encode reviewed diff: %w", err)
	}
	human := childRevision(source, params.HumanRevisionID, source.ID, transcript.KindHumanEdited,
		humanText, "reviewed", params.ReviewerID, reviewDiff, humanSegments, now)
	approvedDiff := json.RawMessage(`{"changes":[]}`)
	approved := childRevision(source, params.ApprovedRevisionID, human.ID, transcript.KindApproved,
		humanText, "approved", params.ReviewerID, approvedDiff, approvedSegments, now)
	if err := insertRevision(ctx, tx, human); err != nil {
		return ApprovalResult{}, err
	}
	if err := insertSegments(ctx, tx, human.ID, human.Segments, now); err != nil {
		return ApprovalResult{}, err
	}
	if err := insertRevision(ctx, tx, approved); err != nil {
		return ApprovalResult{}, err
	}
	if err := insertSegments(ctx, tx, approved.ID, approved.Segments, now); err != nil {
		return ApprovalResult{}, err
	}
	metadata, err := json.Marshal(struct {
		AcceptPending bool  `json:"accept_pending"`
		Accepted      []int `json:"accepted_change_indexes"`
	}{params.AcceptPending, acceptedIndexes})
	if err != nil {
		return ApprovalResult{}, fmt.Errorf("encode approval metadata: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO transcript_revision_reviews (
			id, workspace_id, revision_id, reviewer_id, action,
			resulting_revision_id, metadata, created_at
		) VALUES ($1, $2, $3, $4, 'approve', $5, $6, $7)`,
		params.ReviewID, params.WorkspaceID, source.ID, params.ReviewerID,
		approved.ID, metadata, now)
	if uniqueViolation(err) {
		return ApprovalResult{}, ErrConflict
	}
	if err != nil {
		return ApprovalResult{}, fmt.Errorf("insert approval review: %w", err)
	}
	if err := insertAudit(ctx, tx, params.AuditID, params.WorkspaceID, params.ReviewerID,
		"transcript.approved", approved.ID, metadata, now); err != nil {
		return ApprovalResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ApprovalResult{}, fmt.Errorf("commit approval: %w", err)
	}
	record := Record{
		ID: params.ReviewID, RevisionID: source.ID, ReviewerID: params.ReviewerID,
		Action: ActionApprove, ResultingRevisionID: approved.ID, CreatedAt: now,
	}
	return ApprovalResult{ReviewRecord: record, HumanRevision: human, ApprovedRevision: approved}, nil
}

func loadRevision(ctx context.Context, tx pgx.Tx, workspaceID, revisionID string) (transcript.Revision, error) {
	var result transcript.Revision
	var parentID, rawObjectID, sourceJobID, createdBy, model, prompt *string
	var provider, hotword, glossary, diff, validation []byte
	err := tx.QueryRow(ctx, `
		SELECT revision.id::text, revision.transcript_id::text, transcript.asset_id::text,
		       revision.parent_revision_id::text, revision.kind, transcript.language,
		       revision.text_content, revision.provider_snapshot, revision.hotword_snapshot,
		       revision.glossary_snapshot, revision.diff, revision.validation_result,
		       revision.provider_raw_object_id::text, revision.source_job_id::text,
		       revision.created_by::text, revision.created_by_type, revision.model,
		       revision.prompt_version, revision.review_status, revision.created_at
		FROM transcript_revisions revision
		JOIN transcripts transcript ON transcript.id = revision.transcript_id
		JOIN assets asset ON asset.id = transcript.asset_id
		WHERE revision.id = $1 AND asset.workspace_id = $2 AND asset.deleted_at IS NULL
		FOR KEY SHARE OF revision`, revisionID, workspaceID).Scan(
		&result.ID, &result.TranscriptID, &result.AssetID, &parentID, &result.Kind,
		&result.Language, &result.Text, &provider, &hotword, &glossary, &diff,
		&validation, &rawObjectID, &sourceJobID, &createdBy, &result.CreatedByType,
		&model, &prompt, &result.ReviewStatus, &result.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return transcript.Revision{}, ErrNotFound
	}
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("load review revision: %w", err)
	}
	assignRevisionOptionals(&result, parentID, rawObjectID, sourceJobID, createdBy, model, prompt)
	result.ProviderSnapshot, result.HotwordSnapshot = provider, hotword
	result.GlossarySnapshot, result.Diff, result.ValidationResult = glossary, diff, validation
	rows, err := tx.Query(ctx, `
		SELECT id::text, ordinal, start_ms, end_ms, speaker, text_content, confidence, words
		FROM transcript_segments WHERE revision_id = $1 ORDER BY ordinal`, revisionID)
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("load review segments: %w", err)
	}
	defer rows.Close()
	result.Segments = make([]transcript.Segment, 0)
	for rows.Next() {
		var segment transcript.Segment
		var words []byte
		if err := rows.Scan(&segment.ID, &segment.Ordinal, &segment.StartMS, &segment.EndMS,
			&segment.Speaker, &segment.Text, &segment.Confidence, &words); err != nil {
			return transcript.Revision{}, fmt.Errorf("scan review segment: %w", err)
		}
		segment.Words = append(json.RawMessage(nil), words...)
		result.Segments = append(result.Segments, segment)
	}
	return result, rows.Err()
}

func decodeChanges(raw json.RawMessage) ([]llm.Change, error) {
	var diff struct {
		Changes []llm.Change `json:"changes"`
	}
	if err := json.Unmarshal(raw, &diff); err != nil || diff.Changes == nil {
		return nil, errors.New("invalid correction diff")
	}
	return diff.Changes, nil
}

func loadDecisions(ctx context.Context, tx pgx.Tx, workspaceID, revisionID string) ([]Record, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, revision_id::text, reviewer_id::text, action,
		       change_index, created_at
		FROM transcript_revision_reviews
		WHERE workspace_id = $1 AND revision_id = $2 AND action <> 'approve'
		ORDER BY created_at, id`, workspaceID, revisionID)
	if err != nil {
		return nil, fmt.Errorf("load review decisions: %w", err)
	}
	defer rows.Close()
	result := make([]Record, 0)
	for rows.Next() {
		var record Record
		if err := rows.Scan(&record.ID, &record.RevisionID, &record.ReviewerID,
			&record.Action, &record.ChangeIndex, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan review decision: %w", err)
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func buildReviewedContent(parent, corrected transcript.Revision, changes []llm.Change, accepted []bool, ids []string) (string, []transcript.Segment, []int, error) {
	changeBySegment := make(map[string]int, len(changes))
	for index, change := range changes {
		changeBySegment[change.SegmentID] = index
	}
	segments := make([]transcript.Segment, len(parent.Segments))
	acceptedIndexes := make([]int, 0)
	var text strings.Builder
	cursor := 0
	for ordinal, original := range parent.Segments {
		candidate := cloneSegment(original)
		candidate.ID = ids[ordinal]
		position := strings.Index(parent.Text[cursor:], original.Text)
		if position < 0 || ordinal >= len(corrected.Segments) ||
			corrected.Segments[ordinal].StartMS != original.StartMS || corrected.Segments[ordinal].EndMS != original.EndMS {
			return "", nil, nil, errors.New("revision timelines do not align")
		}
		position += cursor
		text.WriteString(parent.Text[cursor:position])
		if index, changed := changeBySegment[original.ID]; changed {
			change := changes[index]
			if change.Original != original.Text || corrected.Segments[ordinal].Text != change.Replacement {
				return "", nil, nil, errors.New("correction diff does not match revisions")
			}
			if accepted[index] {
				candidate = cloneSegment(corrected.Segments[ordinal])
				candidate.ID = ids[ordinal]
				acceptedIndexes = append(acceptedIndexes, index)
			}
		}
		candidate.Ordinal = ordinal
		segments[ordinal] = candidate
		text.WriteString(candidate.Text)
		cursor = position + len(original.Text)
	}
	text.WriteString(parent.Text[cursor:])
	return text.String(), segments, acceptedIndexes, nil
}

func childRevision(source transcript.Revision, id, parentID, kind, text, status, actorID string,
	diff json.RawMessage, segments []transcript.Segment, now time.Time) transcript.Revision {
	return transcript.Revision{
		ID: id, TranscriptID: source.TranscriptID, AssetID: source.AssetID,
		ParentRevisionID: parentID, Kind: kind, Language: source.Language, Text: text,
		ProviderSnapshot: append(json.RawMessage(nil), source.ProviderSnapshot...),
		HotwordSnapshot:  append(json.RawMessage(nil), source.HotwordSnapshot...),
		GlossarySnapshot: append(json.RawMessage(nil), source.GlossarySnapshot...),
		Diff:             append(json.RawMessage(nil), diff...), ValidationResult: append(json.RawMessage(nil), source.ValidationResult...),
		ProviderRawObjectID: source.ProviderRawObjectID, CreatedBy: actorID,
		CreatedByType: "user", Model: source.Model, PromptVersion: source.PromptVersion,
		ReviewStatus: status, CreatedAt: now, Segments: segments,
	}
}

func insertRevision(ctx context.Context, tx pgx.Tx, revision transcript.Revision) error {
	var rawObjectID any
	if revision.ProviderRawObjectID != "" {
		rawObjectID = revision.ProviderRawObjectID
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO transcript_revisions (
			id, transcript_id, parent_revision_id, kind, text_content,
			provider_snapshot, hotword_snapshot, glossary_snapshot, diff,
			validation_result, created_by, created_by_type, provider_raw_object_id,
			model, prompt_version, review_status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		          $11, 'user', $12, $13, $14, $15, $16)`,
		revision.ID, revision.TranscriptID, revision.ParentRevisionID, revision.Kind,
		revision.Text, revision.ProviderSnapshot, revision.HotwordSnapshot,
		revision.GlossarySnapshot, revision.Diff, revision.ValidationResult,
		revision.CreatedBy, rawObjectID, revision.Model, revision.PromptVersion,
		revision.ReviewStatus, revision.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert %s revision: %w", revision.Kind, err)
	}
	return nil
}

func insertSegments(ctx context.Context, tx pgx.Tx, revisionID string, segments []transcript.Segment, now time.Time) error {
	for _, segment := range segments {
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcript_segments (
				id, revision_id, ordinal, start_ms, end_ms, speaker,
				text_content, confidence, words, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			segment.ID, revisionID, segment.Ordinal, segment.StartMS, segment.EndMS,
			segment.Speaker, segment.Text, segment.Confidence, segment.Words, now); err != nil {
			return fmt.Errorf("insert %s segment: %w", revisionID, err)
		}
	}
	return nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, auditID, workspaceID, actorID, action, targetID string, metadata []byte, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, metadata, occurred_at
		) VALUES ($1, $2, $3, 'user', $4, 'transcript_revision', $5, $6, $7)`,
		auditID, workspaceID, actorID, action, targetID, metadata, now); err != nil {
		return fmt.Errorf("insert review audit: %w", err)
	}
	return nil
}

func cloneWithIDs(segments []transcript.Segment, ids []string) []transcript.Segment {
	result := make([]transcript.Segment, len(segments))
	for index, segment := range segments {
		result[index] = cloneSegment(segment)
		result[index].ID = ids[index]
	}
	return result
}

func cloneSegment(segment transcript.Segment) transcript.Segment {
	result := segment
	result.Words = append(json.RawMessage(nil), segment.Words...)
	if segment.Speaker != nil {
		value := *segment.Speaker
		result.Speaker = &value
	}
	if segment.Confidence != nil {
		value := *segment.Confidence
		result.Confidence = &value
	}
	return result
}

func assignRevisionOptionals(result *transcript.Revision, parentID, rawObjectID, sourceJobID, createdBy, model, prompt *string) {
	if parentID != nil {
		result.ParentRevisionID = *parentID
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
	if prompt != nil {
		result.PromptVersion = *prompt
	}
}

func uniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
