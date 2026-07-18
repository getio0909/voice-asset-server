package transcription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/transcript"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidCommit = errors.New("invalid raw transcript commit")
	commitSHA256     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	commitProviderID = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
)

type PostgresCommitter struct {
	pool *pgxpool.Pool
}

func NewPostgresCommitter(pool *pgxpool.Pool) *PostgresCommitter {
	return &PostgresCommitter{pool: pool}
}

// CommitRaw atomically publishes the provider response, normalized revision,
// timeline, successful attempt, job result, asset state, and audit record.
func (c *PostgresCommitter) CommitRaw(
	ctx context.Context,
	params CommitRawParams,
) (transcript.Revision, error) {
	if err := validateCommit(params); err != nil {
		return transcript.Revision{}, err
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("begin transcript commit: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tx.QueryRow(ctx, `
		SELECT GREATEST($1::timestamptz, clock_timestamp())`, params.Now,
	).Scan(&params.Now); err != nil {
		return transcript.Revision{}, fmt.Errorf("resolve transcript commit clock: %w", err)
	}

	var (
		state          string
		workspaceID    string
		assetID        string
		createdBy      string
		leaseOwner     *string
		leaseExpiresAt *time.Time
		attempt        int
	)
	err = tx.QueryRow(ctx, `
		SELECT state, workspace_id::text, asset_id::text, created_by::text,
		       lease_owner, lease_expires_at, attempts
		FROM jobs
		WHERE id = $1
		FOR UPDATE`, params.JobID).Scan(
		&state, &workspaceID, &assetID, &createdBy,
		&leaseOwner, &leaseExpiresAt, &attempt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return transcript.Revision{}, job.ErrNotFound
	}
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("lock transcript job: %w", err)
	}
	if state != job.StateRunning || workspaceID != params.WorkspaceID ||
		assetID != params.AssetID || createdBy != params.ActorID ||
		leaseOwner == nil || *leaseOwner != params.WorkerID ||
		leaseExpiresAt == nil || !leaseExpiresAt.After(params.Now) {
		return transcript.Revision{}, job.ErrLeaseConflict
	}

	var assetState string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM assets
		WHERE id = $1 AND workspace_id = $2 AND deleted_at IS NULL
		FOR UPDATE`, params.AssetID, params.WorkspaceID).Scan(&assetState); errors.Is(err, pgx.ErrNoRows) {
		return transcript.Revision{}, job.ErrNotFound
	} else if err != nil {
		return transcript.Revision{}, fmt.Errorf("lock transcript asset: %w", err)
	}
	if assetState != "processing" {
		return transcript.Revision{}, ErrInvalidCommit
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_objects (
			id, asset_id, kind, storage_backend, storage_key, mime_type,
			file_size, sha256, creation_source, encryption_state, created_at
		) VALUES (
			$1, $2, 'provider_raw_response', $3, $4, 'application/json',
			$5, $6, $7, 'none', $8
		)`,
		params.RawObjectID, params.AssetID, params.RawObject.Backend, params.RawObject.Key,
		params.RawObject.Size, params.RawObject.SHA256, params.ProviderID, params.Now,
	); err != nil {
		return transcript.Revision{}, fmt.Errorf("insert provider raw object: %w", err)
	}

	transcriptID := ""
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM transcripts
		WHERE asset_id = $1
		ORDER BY created_at, id
		LIMIT 1
		FOR UPDATE`, params.AssetID).Scan(&transcriptID)
	if errors.Is(err, pgx.ErrNoRows) {
		transcriptID = params.TranscriptID
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcripts (id, asset_id, language, created_at)
			VALUES ($1, $2, $3, $4)`,
			transcriptID, params.AssetID, params.Language, params.Now,
		); err != nil {
			return transcript.Revision{}, fmt.Errorf("insert transcript: %w", err)
		}
	} else if err != nil {
		return transcript.Revision{}, fmt.Errorf("find asset transcript: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO transcript_revisions (
			id, transcript_id, kind, text_content, provider_snapshot, hotword_snapshot,
			created_by, created_by_type, source_job_id, provider_raw_object_id, created_at
		) VALUES ($1, $2, 'raw_asr', $3, $4, $5, $6, 'system', $7, $8, $9)`,
		params.RevisionID, transcriptID, params.Text, []byte(params.ProviderSnapshot),
		[]byte(params.HotwordSnapshot), params.ActorID, params.JobID, params.RawObjectID, params.Now,
	); err != nil {
		return transcript.Revision{}, fmt.Errorf("insert raw transcript revision: %w", err)
	}
	for _, segment := range params.Segments {
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcript_segments (
				id, revision_id, ordinal, start_ms, end_ms, speaker,
				text_content, confidence, words, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			segment.ID, params.RevisionID, segment.Ordinal,
			segment.StartMS, segment.EndMS, segment.Speaker,
			segment.Text, segment.Confidence, []byte(segment.Words), params.Now,
		); err != nil {
			return transcript.Revision{}, fmt.Errorf("insert transcript segment: %w", err)
		}
	}
	normalizedAt := params.Now.Add(time.Microsecond)
	normalizationDiff := json.RawMessage(`{"changes":[]}`)
	normalizationValidation := json.RawMessage(`{"normalizer":"identity_v1","valid":true}`)
	if _, err := tx.Exec(ctx, `
		INSERT INTO transcript_revisions (
			id, transcript_id, parent_revision_id, kind, text_content,
			provider_snapshot, hotword_snapshot, diff, validation_result,
			created_by, created_by_type, source_job_id, review_status, created_at
		) VALUES (
			$1, $2, $3, 'normalized', $4, $5, $6, $7, $8,
			$9, 'system', $10, 'pending', $11
		)`, params.NormalizedRevisionID, transcriptID, params.RevisionID, params.Text,
		params.ProviderSnapshot, params.HotwordSnapshot, normalizationDiff,
		normalizationValidation, params.ActorID, params.JobID, normalizedAt); err != nil {
		return transcript.Revision{}, fmt.Errorf("insert normalized transcript revision: %w", err)
	}
	for _, segment := range params.NormalizedSegments {
		if _, err := tx.Exec(ctx, `
			INSERT INTO transcript_segments (
				id, revision_id, ordinal, start_ms, end_ms, speaker,
				text_content, confidence, words, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			segment.ID, params.NormalizedRevisionID, segment.Ordinal,
			segment.StartMS, segment.EndMS, segment.Speaker,
			segment.Text, segment.Confidence, []byte(segment.Words), normalizedAt,
		); err != nil {
			return transcript.Revision{}, fmt.Errorf("insert normalized transcript segment: %w", err)
		}
	}

	commandTag, err := tx.Exec(ctx, `
		UPDATE job_attempts
		SET outcome = 'succeeded', error_code = NULL, finished_at = $3
		WHERE job_id = $1 AND attempt = $2 AND outcome IS NULL`,
		params.JobID, attempt, normalizedAt,
	)
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("finish transcript job attempt: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return transcript.Revision{}, job.ErrLeaseConflict
	}
	commandTag, err = tx.Exec(ctx, `
		UPDATE jobs
		SET state = 'succeeded', lease_owner = NULL, lease_expires_at = NULL,
		    last_error_code = NULL, result_revision_id = $2, updated_at = $3
		WHERE id = $1 AND state = 'running'`,
		params.JobID, params.NormalizedRevisionID, normalizedAt,
	)
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("succeed transcript job: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return transcript.Revision{}, job.ErrLeaseConflict
	}
	commandTag, err = tx.Exec(ctx, `
		UPDATE assets
		SET status = 'ready', version = version + 1, updated_at = $3
		WHERE id = $1 AND workspace_id = $2 AND status = 'processing'`,
		params.AssetID, params.WorkspaceID, normalizedAt,
	)
	if err != nil {
		return transcript.Revision{}, fmt.Errorf("mark transcribed asset ready: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return transcript.Revision{}, ErrInvalidCommit
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (
			id, workspace_id, actor_id, actor_type, action,
			target_type, target_id, metadata, occurred_at
		) VALUES (
			$1, $2, NULL, 'system', 'transcription.completed',
			'transcript_revision', $3,
			jsonb_build_object(
				'job_id', $4::text, 'requested_by', $5::text,
				'raw_revision_id', $6::text, 'normalized_revision_id', $3::uuid::text
			), $7
		)`,
		params.AuditID, params.WorkspaceID, params.NormalizedRevisionID,
		params.JobID, params.ActorID, params.RevisionID, normalizedAt,
	); err != nil {
		return transcript.Revision{}, fmt.Errorf("insert transcription audit log: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return transcript.Revision{}, fmt.Errorf("commit raw transcript: %w", err)
	}

	return transcript.Revision{
		ID: params.NormalizedRevisionID, TranscriptID: transcriptID, AssetID: params.AssetID,
		ParentRevisionID: params.RevisionID, Kind: transcript.KindNormalized,
		Language: params.Language, Text: params.Text,
		ProviderSnapshot: append(json.RawMessage(nil), params.ProviderSnapshot...),
		HotwordSnapshot:  append(json.RawMessage(nil), params.HotwordSnapshot...),
		Diff:             append(json.RawMessage(nil), normalizationDiff...),
		ValidationResult: append(json.RawMessage(nil), normalizationValidation...),
		SourceJobID:      params.JobID, CreatedBy: params.ActorID, CreatedByType: "system",
		ReviewStatus: "pending", CreatedAt: normalizedAt,
		Segments: cloneSegments(params.NormalizedSegments),
	}, nil
}

func validateCommit(params CommitRawParams) error {
	if params.JobID == "" || params.WorkerID == "" || params.WorkspaceID == "" ||
		params.AssetID == "" || params.ActorID == "" || params.TranscriptID == "" ||
		params.RevisionID == "" || params.NormalizedRevisionID == "" ||
		params.RawObjectID == "" || params.AuditID == "" ||
		params.Language == "" || params.Text == "" || params.Now.IsZero() ||
		!commitProviderID.MatchString(params.ProviderID) ||
		params.RawObject.Key == "" || params.RawObject.Size <= 0 ||
		!commitSHA256.MatchString(params.RawObject.SHA256) ||
		!validJSONObject(params.ProviderSnapshot) || !validJSONObject(params.HotwordSnapshot) ||
		len(params.Segments) == 0 || len(params.NormalizedSegments) != len(params.Segments) {
		return ErrInvalidCommit
	}
	for ordinal, segment := range params.Segments {
		var words []json.RawMessage
		if segment.ID == "" || segment.Ordinal != ordinal || segment.StartMS < 0 ||
			segment.EndMS < segment.StartMS || segment.Text == "" ||
			segment.Confidence == nil || *segment.Confidence < 0 || *segment.Confidence > 1 ||
			json.Unmarshal(segment.Words, &words) != nil {
			return ErrInvalidCommit
		}
	}
	for ordinal, segment := range params.NormalizedSegments {
		if segment.ID == "" || segment.ID == params.Segments[ordinal].ID ||
			segment.Ordinal != ordinal || segment.StartMS != params.Segments[ordinal].StartMS ||
			segment.EndMS != params.Segments[ordinal].EndMS || segment.Text != params.Segments[ordinal].Text ||
			!bytes.Equal(segment.Words, params.Segments[ordinal].Words) {
			return ErrInvalidCommit
		}
	}
	return nil
}

func validJSONObject(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 1 && trimmed[0] == '{' && json.Valid(trimmed)
}

func cloneSegments(input []transcript.Segment) []transcript.Segment {
	result := make([]transcript.Segment, len(input))
	for index, segment := range input {
		result[index] = segment
		result[index].Words = append(json.RawMessage(nil), segment.Words...)
		if segment.Speaker != nil {
			value := *segment.Speaker
			result[index].Speaker = &value
		}
		if segment.Confidence != nil {
			value := *segment.Confidence
			result[index].Confidence = &value
		}
	}
	return result
}
