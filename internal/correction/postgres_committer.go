package correction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/transcript"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidCommit = errors.New("invalid correction commit")
	sha256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	providerPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
)

type PostgresCommitter struct{ pool *pgxpool.Pool }

func NewPostgresCommitter(pool *pgxpool.Pool) *PostgresCommitter {
	return &PostgresCommitter{pool: pool}
}

func (committer *PostgresCommitter) Commit(ctx context.Context, params CommitParams) (transcript.Revision, error) {
	if err := validateCommit(params); err != nil {
		return transcript.Revision{}, err
	}
	tx, err := committer.pool.Begin(ctx)
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("begin correction commit: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tx.QueryRow(ctx, `SELECT GREATEST($1::timestamptz, clock_timestamp())`, params.Now).Scan(&params.Now); err != nil {
		return transcript.Revision{}, fmt.Errorf("resolve correction commit clock: %w", err)
	}
	var state, workspaceID, assetID, createdBy, kind string
	var leaseOwner *string
	var leaseExpiresAt *time.Time
	var attempt int
	err = tx.QueryRow(ctx, `
		SELECT state, workspace_id::text, asset_id::text, created_by::text, kind,
		       lease_owner, lease_expires_at, attempts
		FROM jobs WHERE id = $1 FOR UPDATE`, params.JobID).Scan(
		&state, &workspaceID, &assetID, &createdBy, &kind,
		&leaseOwner, &leaseExpiresAt, &attempt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return transcript.Revision{}, job.ErrNotFound
	}
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("lock correction job: %w", err)
	}
	if state != job.StateRunning || kind != job.KindLLMCorrect || workspaceID != params.WorkspaceID ||
		assetID != params.AssetID || createdBy != params.ActorID || leaseOwner == nil ||
		*leaseOwner != params.WorkerID || leaseExpiresAt == nil || !leaseExpiresAt.After(params.Now) {
		return transcript.Revision{}, job.ErrLeaseConflict
	}
	var transcriptID, sourceAssetID string
	err = tx.QueryRow(ctx, `
		SELECT revision.transcript_id::text, transcript.asset_id::text
		FROM transcript_revisions revision
		JOIN transcripts transcript ON transcript.id = revision.transcript_id
		JOIN assets asset ON asset.id = transcript.asset_id
		WHERE revision.id = $1 AND asset.workspace_id = $2 AND asset.deleted_at IS NULL
		FOR KEY SHARE OF revision`, params.SourceRevisionID, params.WorkspaceID,
	).Scan(&transcriptID, &sourceAssetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return transcript.Revision{}, job.ErrNotFound
	}
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("lock correction source: %w", err)
	}
	if transcriptID != params.TranscriptID || sourceAssetID != params.AssetID {
		return transcript.Revision{}, ErrInvalidCommit
	}
	reviewStatus := "pending"
	if params.AutoApproval != nil {
		reviewStatus = "approved"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			file_size, sha256, creation_source, encryption_state, created_at
		) VALUES (
			$1, $2, 'provider_raw_response', $3, $4, 'application/json',
			$5, $6, $7, 'none', $8
		)`, params.RawObjectID, params.AssetID, params.RawObject.Backend, params.RawObject.Key,
		params.RawObject.Size, params.RawObject.SHA256, params.ProviderID, params.Now); err != nil {
		return transcript.Revision{}, fmt.Errorf("insert LLM raw object: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcript_revisions (
			id, transcript_id, parent_revision_id, kind, text_content,
			provider_snapshot, hotword_snapshot, glossary_snapshot, diff,
			validation_result, created_by, created_by_type, source_job_id,
			provider_raw_object_id, model, prompt_version, review_status, created_at
		) VALUES (
			$1, $2, $3, 'llm_corrected', $4, $5, $6, $7, $8, $9,
			$10, 'system', $11, $12, $13, $14, $15, $16
		)`, params.RevisionID, params.TranscriptID, params.SourceRevisionID, params.Text,
		params.ProviderSnapshot, params.HotwordSnapshot, params.GlossarySnapshot,
		params.Diff, params.ValidationResult, params.ActorID, params.JobID,
		params.RawObjectID, params.Model, params.PromptVersion, reviewStatus, params.Now); err != nil {
		return transcript.Revision{}, fmt.Errorf("insert corrected revision: %w", err)
	}
	for _, segment := range params.Segments {
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcript_segments (
				id, revision_id, ordinal, start_ms, end_ms, speaker,
				text_content, confidence, words, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			segment.ID, params.RevisionID, segment.Ordinal, segment.StartMS, segment.EndMS,
			segment.Speaker, segment.Text, segment.Confidence, segment.Words, params.Now); err != nil {
			return transcript.Revision{}, fmt.Errorf("insert corrected segment: %w", err)
		}
	}
	corrected := correctedRevision(params, reviewStatus)
	result := corrected
	if params.AutoApproval != nil {
		_, approved, err := commitAutoApproval(ctx, tx, params, corrected)
		if err != nil {
			return transcript.Revision{}, err
		}
		result = approved
	}
	command, err := tx.Exec(ctx, `
		UPDATE job_attempts SET outcome = 'succeeded', error_code = NULL, finished_at = $3
		WHERE job_id = $1 AND attempt = $2 AND outcome IS NULL`, params.JobID, attempt, params.Now)
	if err != nil || command.RowsAffected() != 1 {
		return transcript.Revision{}, job.ErrLeaseConflict
	}
	command, err = tx.Exec(ctx, `
		UPDATE jobs SET state = 'succeeded', lease_owner = NULL, lease_expires_at = NULL,
		    last_error_code = NULL, result_revision_id = $2, updated_at = $3
		WHERE id = $1 AND state = 'running'`, params.JobID, result.ID, params.Now)
	if err != nil || command.RowsAffected() != 1 {
		return transcript.Revision{}, job.ErrLeaseConflict
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, metadata, occurred_at
		) VALUES (
			$1, $2, NULL, 'system', 'correction.completed', 'transcript_revision',
			$3, jsonb_build_object('job_id', $4::text, 'source_revision_id', $5::text,
			'requested_by', $6::text), $7
		)`, params.AuditID, params.WorkspaceID, params.RevisionID, params.JobID,
		params.SourceRevisionID, params.ActorID, params.Now); err != nil {
		return transcript.Revision{}, fmt.Errorf("insert correction audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return transcript.Revision{}, fmt.Errorf("commit corrected revision: %w", err)
	}
	return result, nil
}

func correctedRevision(params CommitParams, reviewStatus string) transcript.Revision {
	return transcript.Revision{
		ID: params.RevisionID, TranscriptID: params.TranscriptID, AssetID: params.AssetID,
		ParentRevisionID: params.SourceRevisionID, Kind: transcript.KindLLMCorrected,
		Language: params.Language, Text: params.Text,
		ProviderSnapshot: append(json.RawMessage(nil), params.ProviderSnapshot...),
		HotwordSnapshot:  append(json.RawMessage(nil), params.HotwordSnapshot...),
		GlossarySnapshot: append(json.RawMessage(nil), params.GlossarySnapshot...),
		Diff:             append(json.RawMessage(nil), params.Diff...), ValidationResult: append(json.RawMessage(nil), params.ValidationResult...),
		ProviderRawObjectID: params.RawObjectID, SourceJobID: params.JobID,
		CreatedBy: params.ActorID, CreatedByType: "system", Model: params.Model,
		PromptVersion: params.PromptVersion, ReviewStatus: reviewStatus,
		CreatedAt: params.Now, Segments: cloneSegments(params.Segments),
	}
}

func validateCommit(params CommitParams) error {
	if params.JobID == "" || params.WorkerID == "" || params.WorkspaceID == "" ||
		params.AssetID == "" || params.ActorID == "" || params.SourceRevisionID == "" ||
		params.RevisionID == "" || params.RawObjectID == "" || params.AuditID == "" ||
		params.TranscriptID == "" || params.Language == "" || params.Model == "" ||
		params.PromptVersion == "" || params.Now.IsZero() || !providerPattern.MatchString(params.ProviderID) ||
		params.RawObject.Key == "" || params.RawObject.Size <= 0 || !sha256Pattern.MatchString(params.RawObject.SHA256) ||
		!validObject(params.ProviderSnapshot) || !validObject(params.HotwordSnapshot) ||
		!validObject(params.GlossarySnapshot) || !validObject(params.Diff) ||
		!validObject(params.ValidationResult) || len(params.Segments) == 0 {
		return ErrInvalidCommit
	}
	for ordinal, segment := range params.Segments {
		if segment.ID == "" || segment.Ordinal != ordinal || segment.StartMS < 0 ||
			segment.EndMS < segment.StartMS || segment.Text == "" || !json.Valid(segment.Words) {
			return ErrInvalidCommit
		}
	}
	if err := validateAutoApproval(params); err != nil {
		return err
	}
	return nil
}

func validateAutoApproval(params CommitParams) error {
	auto := params.AutoApproval
	if auto == nil {
		return nil
	}
	if auto.Policy != llm.AutoApprovalGlossaryOnly || auto.ReviewID == "" || auto.AuditID == "" ||
		auto.HumanRevisionID == "" || auto.ApprovedRevisionID == "" ||
		len(auto.HumanSegmentIDs) != len(params.Segments) ||
		len(auto.ApprovedSegmentIDs) != len(params.Segments) {
		return ErrInvalidCommit
	}
	var validation llm.ValidationResult
	if err := json.Unmarshal(params.ValidationResult, &validation); err != nil ||
		!autoApprovalValidationPassed(validation) {
		return ErrInvalidCommit
	}
	var diff struct {
		Changes []llm.Change `json:"changes"`
	}
	if err := json.Unmarshal(params.Diff, &diff); err != nil || len(diff.Changes) == 0 {
		return ErrInvalidCommit
	}
	var glossarySnapshot struct {
		Sets []json.RawMessage `json:"sets"`
	}
	if err := json.Unmarshal(params.GlossarySnapshot, &glossarySnapshot); err != nil ||
		len(glossarySnapshot.Sets) == 0 {
		return ErrInvalidCommit
	}
	var providerSnapshot struct {
		AutoApprovalPolicy string `json:"auto_approval_policy"`
	}
	if err := json.Unmarshal(params.ProviderSnapshot, &providerSnapshot); err != nil ||
		providerSnapshot.AutoApprovalPolicy != auto.Policy {
		return ErrInvalidCommit
	}
	seen := map[string]struct{}{
		params.RevisionID: {}, params.RawObjectID: {}, params.AuditID: {},
	}
	for _, segment := range params.Segments {
		seen[segment.ID] = struct{}{}
	}
	ids := []string{auto.ReviewID, auto.AuditID, auto.HumanRevisionID, auto.ApprovedRevisionID}
	ids = append(ids, auto.HumanSegmentIDs...)
	ids = append(ids, auto.ApprovedSegmentIDs...)
	for _, id := range ids {
		if id == "" {
			return ErrInvalidCommit
		}
		if _, exists := seen[id]; exists {
			return ErrInvalidCommit
		}
		seen[id] = struct{}{}
	}
	return nil
}

func commitAutoApproval(ctx context.Context, tx pgx.Tx, params CommitParams,
	corrected transcript.Revision) (transcript.Revision, transcript.Revision, error) {
	auto := params.AutoApproval
	if auto == nil {
		return transcript.Revision{}, transcript.Revision{}, ErrInvalidCommit
	}
	var diff struct {
		Changes []llm.Change `json:"changes"`
	}
	if err := json.Unmarshal(params.Diff, &diff); err != nil {
		return transcript.Revision{}, transcript.Revision{}, ErrInvalidCommit
	}
	accepted := make([]int, len(diff.Changes))
	for index := range accepted {
		accepted[index] = index
	}
	metadata, err := json.Marshal(struct {
		AcceptPending      bool   `json:"accept_pending"`
		Accepted           []int  `json:"accepted_change_indexes"`
		Automated          bool   `json:"automated"`
		AutoApprovalPolicy string `json:"auto_approval_policy"`
		RequestedBy        string `json:"requested_by"`
	}{true, accepted, true, auto.Policy, params.ActorID})
	if err != nil {
		return transcript.Revision{}, transcript.Revision{}, fmt.Errorf("encode auto-approval metadata: %w", err)
	}
	humanDiff, err := json.Marshal(struct {
		SourceRevisionID      string `json:"source_revision_id"`
		AcceptedChangeIndexes []int  `json:"accepted_change_indexes"`
		Automated             bool   `json:"automated"`
		AutoApprovalPolicy    string `json:"auto_approval_policy"`
	}{corrected.ID, accepted, true, auto.Policy})
	if err != nil {
		return transcript.Revision{}, transcript.Revision{}, fmt.Errorf("encode auto-reviewed diff: %w", err)
	}
	humanSegments := cloneSegmentsWithIDs(corrected.Segments, auto.HumanSegmentIDs)
	approvedSegments := cloneSegmentsWithIDs(humanSegments, auto.ApprovedSegmentIDs)
	human := autoApprovalChild(corrected, auto.HumanRevisionID, corrected.ID,
		transcript.KindHumanEdited, "reviewed", humanDiff, humanSegments, params)
	approved := autoApprovalChild(corrected, auto.ApprovedRevisionID, human.ID,
		transcript.KindApproved, "approved", json.RawMessage(`{"changes":[]}`), approvedSegments, params)
	if err := insertAutoApprovalRevision(ctx, tx, human); err != nil {
		return transcript.Revision{}, transcript.Revision{}, err
	}
	if err := insertAutoApprovalSegments(ctx, tx, human.ID, human.Segments, params.Now); err != nil {
		return transcript.Revision{}, transcript.Revision{}, err
	}
	if err := insertAutoApprovalRevision(ctx, tx, approved); err != nil {
		return transcript.Revision{}, transcript.Revision{}, err
	}
	if err := insertAutoApprovalSegments(ctx, tx, approved.ID, approved.Segments, params.Now); err != nil {
		return transcript.Revision{}, transcript.Revision{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcript_revision_reviews (
			id, workspace_id, revision_id, reviewer_id, action,
			resulting_revision_id, metadata, created_at
		) VALUES ($1, $2, $3, $4, 'approve', $5, $6, $7)`,
		auto.ReviewID, params.WorkspaceID, corrected.ID, params.ActorID,
		approved.ID, metadata, params.Now); err != nil {
		return transcript.Revision{}, transcript.Revision{}, fmt.Errorf("insert auto-approval review: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action, target_type,
			target_id, metadata, occurred_at
		) VALUES ($1, $2, NULL, 'system', 'transcript.auto_approved',
			'transcript_revision', $3, $4, $5)`,
		auto.AuditID, params.WorkspaceID, approved.ID, metadata, params.Now); err != nil {
		return transcript.Revision{}, transcript.Revision{}, fmt.Errorf("insert auto-approval audit: %w", err)
	}
	return human, approved, nil
}

func autoApprovalChild(source transcript.Revision, id, parentID, kind, status string,
	diff json.RawMessage, segments []transcript.Segment, params CommitParams) transcript.Revision {
	return transcript.Revision{
		ID: id, TranscriptID: source.TranscriptID, AssetID: source.AssetID,
		ParentRevisionID: parentID, Kind: kind, Language: source.Language, Text: source.Text,
		ProviderSnapshot: append(json.RawMessage(nil), source.ProviderSnapshot...),
		HotwordSnapshot:  append(json.RawMessage(nil), source.HotwordSnapshot...),
		GlossarySnapshot: append(json.RawMessage(nil), source.GlossarySnapshot...),
		Diff:             append(json.RawMessage(nil), diff...), ValidationResult: append(json.RawMessage(nil), source.ValidationResult...),
		ProviderRawObjectID: source.ProviderRawObjectID, CreatedBy: params.ActorID,
		CreatedByType: "system", Model: source.Model, PromptVersion: source.PromptVersion,
		ReviewStatus: status, CreatedAt: params.Now, Segments: segments,
	}
}

func insertAutoApprovalRevision(ctx context.Context, tx pgx.Tx, revision transcript.Revision) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO transcript_revisions (
			id, transcript_id, parent_revision_id, kind, text_content,
			provider_snapshot, hotword_snapshot, glossary_snapshot, diff,
			validation_result, created_by, created_by_type, provider_raw_object_id,
			model, prompt_version, review_status, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
		          $11, 'system', $12, $13, $14, $15, $16)`,
		revision.ID, revision.TranscriptID, revision.ParentRevisionID, revision.Kind,
		revision.Text, revision.ProviderSnapshot, revision.HotwordSnapshot,
		revision.GlossarySnapshot, revision.Diff, revision.ValidationResult,
		revision.CreatedBy, revision.ProviderRawObjectID, revision.Model,
		revision.PromptVersion, revision.ReviewStatus, revision.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert auto-approved %s revision: %w", revision.Kind, err)
	}
	return nil
}

func insertAutoApprovalSegments(ctx context.Context, tx pgx.Tx, revisionID string,
	segments []transcript.Segment, now time.Time) error {
	for _, segment := range segments {
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcript_segments (
				id, revision_id, ordinal, start_ms, end_ms, speaker,
				text_content, confidence, words, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			segment.ID, revisionID, segment.Ordinal, segment.StartMS, segment.EndMS,
			segment.Speaker, segment.Text, segment.Confidence, segment.Words, now); err != nil {
			return fmt.Errorf("insert auto-approved %s segment: %w", revisionID, err)
		}
	}
	return nil
}

func cloneSegmentsWithIDs(input []transcript.Segment, ids []string) []transcript.Segment {
	result := cloneSegments(input)
	for index := range result {
		result[index].ID = ids[index]
	}
	return result
}

func validObject(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	return len(value) > 1 && value[0] == '{' && json.Valid(value)
}

func cloneSegments(input []transcript.Segment) []transcript.Segment {
	result := make([]transcript.Segment, len(input))
	for index, segment := range input {
		result[index] = cloneSegment(segment)
	}
	return result
}
