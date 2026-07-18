// Package correction coordinates durable LLM correction jobs, immutable raw
// responses, structured patches, and transcript revision persistence.
package correction

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/glossary"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/llmprofile"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

const (
	DefaultLeaseDuration = 2 * time.Minute
	DefaultRetryDelay    = 30 * time.Second
	maxRawResponseBytes  = 8 * 1024 * 1024
)

var ErrProcessingFailed = errors.New("correction processing failed")

type JobRepository interface {
	Claim(context.Context, job.ClaimParams) (job.Job, error)
	Fail(context.Context, job.FailParams) (job.Job, error)
}

type RevisionRepository interface {
	GetRevision(context.Context, string, string) (transcript.Revision, error)
}

type ProviderResolver interface {
	Resolve(context.Context, string) (llmprofile.ResolvedProvider, error)
}

type GlossaryResolver interface {
	ResolveForAssetWithDefault(context.Context, string, string, string) (glossary.Resolution, error)
}

type RawStore interface {
	PutImmutable(context.Context, string, string, string, io.Reader, int64) (storage.Object, error)
}

type Committer interface {
	Commit(context.Context, CommitParams) (transcript.Revision, error)
}

type CommitParams struct {
	JobID, WorkerID, WorkspaceID, AssetID, ActorID string
	SourceRevisionID, RevisionID, RawObjectID      string
	AuditID, TranscriptID, Language, Text          string
	ProviderID, Model, PromptVersion               string
	ProviderSnapshot, HotwordSnapshot              json.RawMessage
	GlossarySnapshot, Diff, ValidationResult       json.RawMessage
	RawObject                                      storage.Object
	Segments                                       []transcript.Segment
	AutoApproval                                   *AutoApprovalParams
	Now                                            time.Time
}

type AutoApprovalParams struct {
	Policy                              string
	ReviewID, AuditID                   string
	HumanRevisionID, ApprovedRevisionID string
	HumanSegmentIDs, ApprovedSegmentIDs []string
}

type Processor struct {
	jobs       JobRepository
	revisions  RevisionRepository
	providers  ProviderResolver
	glossaries GlossaryResolver
	rawStore   RawStore
	committer  Committer
	workerID   string
	random     io.Reader
	now        func() time.Time
}

func NewProcessor(jobs JobRepository, revisions RevisionRepository, providers ProviderResolver,
	glossaries GlossaryResolver, rawStore RawStore, committer Committer, workerID string) *Processor {
	return &Processor{
		jobs: jobs, revisions: revisions, providers: providers, glossaries: glossaries,
		rawStore: rawStore, committer: committer, workerID: workerID, random: rand.Reader, now: time.Now,
	}
}

func (processor *Processor) RunOnce(ctx context.Context) (bool, error) {
	claimed, err := processor.jobs.Claim(ctx, job.ClaimParams{
		Kind: job.KindLLMCorrect, WorkerID: processor.workerID,
		Now: processor.now().UTC(), LeaseDuration: DefaultLeaseDuration,
	})
	if errors.Is(err, job.ErrNoClaimableJob) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: claim job", ErrProcessingFailed)
	}
	payload, err := decodePayload(claimed.Payload)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "decode job payload")
	}
	source, err := processor.revisions.GetRevision(ctx, claimed.WorkspaceID, payload.SourceRevisionID)
	if err != nil || source.AssetID != claimed.AssetID || len(source.Segments) == 0 {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "load source revision")
	}
	if processor.providers == nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeProviderUnavailable, "resolve LLM provider")
	}
	resolved, err := processor.providers.Resolve(ctx, claimed.WorkspaceID)
	if err != nil || resolved.Provider == nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeProviderUnavailable, "resolve LLM provider")
	}
	glossaryResolution := glossary.Resolution{Snapshot: []byte(`{}`)}
	if processor.glossaries != nil {
		glossaryResolution, err = processor.glossaries.ResolveForAssetWithDefault(
			ctx, claimed.WorkspaceID, claimed.AssetID, resolved.Profile.DefaultGlossaryID,
		)
		if err != nil {
			return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "resolve glossary")
		}
	}
	request := llm.Request{
		Language: source.Language, Glossary: glossaryResolution.Rules,
		Segments: make([]llm.Segment, 0, len(source.Segments)),
	}
	for _, segment := range source.Segments {
		request.Segments = append(request.Segments, llm.Segment{
			ID: segment.ID, StartMS: segment.StartMS, EndMS: segment.EndMS, Text: segment.Text,
		})
	}
	proposal, err := resolved.Provider.Correct(ctx, request)
	if err != nil {
		return true, processor.fail(ctx, claimed, llmJobErrorCode(err), "LLM correction")
	}
	validation, err := llm.ValidateProposal(request, proposal)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeProviderRejected, "validate LLM proposal")
	}
	revisionID, err := processor.newID()
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "generate revision ID")
	}
	rawObjectID, err := processor.newID()
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "generate raw object ID")
	}
	auditID, err := processor.newID()
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "generate audit ID")
	}
	autoApproval, err := processor.buildAutoApproval(resolved.Profile, request, proposal, validation)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "generate auto-approval IDs")
	}
	text, segments, err := processor.applyProposal(source, proposal)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeProviderRejected, "apply LLM proposal")
	}
	rawObject, err := processor.rawStore.PutImmutable(ctx, claimed.AssetID, rawObjectID,
		storage.ObjectKindProviderRawResponse, bytes.NewReader(proposal.RawJSON), maxRawResponseBytes)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "store LLM raw response")
	}
	diff, err := json.Marshal(struct {
		Changes []llm.Change `json:"changes"`
	}{proposal.Changes})
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "encode correction diff")
	}
	validationJSON, err := json.Marshal(validation)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "encode validation result")
	}
	providerSnapshot, err := buildProviderSnapshot(resolved, proposal)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "encode provider snapshot")
	}
	_, err = processor.committer.Commit(ctx, CommitParams{
		JobID: claimed.ID, WorkerID: processor.workerID, WorkspaceID: claimed.WorkspaceID,
		AssetID: claimed.AssetID, ActorID: claimed.CreatedBy, SourceRevisionID: source.ID,
		RevisionID: revisionID, RawObjectID: rawObjectID, AuditID: auditID,
		TranscriptID: source.TranscriptID, Language: source.Language, Text: text,
		ProviderID: proposal.ProviderID, Model: proposal.Model, PromptVersion: proposal.PromptVersion,
		ProviderSnapshot: providerSnapshot, HotwordSnapshot: source.HotwordSnapshot,
		GlossarySnapshot: glossaryResolution.Snapshot, Diff: diff, ValidationResult: validationJSON,
		RawObject: rawObject, Segments: segments, AutoApproval: autoApproval, Now: processor.now().UTC(),
	})
	if err != nil {
		if errors.Is(err, job.ErrLeaseConflict) {
			return true, fmt.Errorf("%w: worker lease was lost", ErrProcessingFailed)
		}
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "commit correction revision")
	}
	return true, nil
}

func (processor *Processor) applyProposal(source transcript.Revision, proposal llm.Proposal) (string, []transcript.Segment, error) {
	changes := make(map[string]llm.Change, len(proposal.Changes))
	for _, change := range proposal.Changes {
		changes[change.SegmentID] = change
	}
	segments := make([]transcript.Segment, 0, len(source.Segments))
	var text strings.Builder
	cursor := 0
	for ordinal, original := range source.Segments {
		position := strings.Index(source.Text[cursor:], original.Text)
		if position < 0 {
			return "", nil, errors.New("source text does not contain ordered segments")
		}
		position += cursor
		text.WriteString(source.Text[cursor:position])
		segment := cloneSegment(original)
		segment.ID = ""
		segment.Ordinal = ordinal
		if change, exists := changes[original.ID]; exists {
			segment.Text = change.Replacement
			segment.Words = json.RawMessage(`[]`)
		}
		segmentID, err := processor.newID()
		if err != nil {
			return "", nil, errors.New("generate segment ID")
		}
		segment.ID = segmentID
		text.WriteString(segment.Text)
		cursor = position + len(original.Text)
		segments = append(segments, segment)
	}
	text.WriteString(source.Text[cursor:])
	return text.String(), segments, nil
}

func buildProviderSnapshot(resolved llmprofile.ResolvedProvider, proposal llm.Proposal) (json.RawMessage, error) {
	return json.Marshal(struct {
		ProviderID         string  `json:"provider_id"`
		ProfileID          string  `json:"profile_id"`
		Model              string  `json:"model"`
		StructuredOutput   bool    `json:"structured_output"`
		Temperature        float64 `json:"temperature"`
		ContextLimit       int     `json:"context_limit"`
		PromptVersion      string  `json:"prompt_version"`
		AutoApprovalPolicy string  `json:"auto_approval_policy"`
	}{
		ProviderID: proposal.ProviderID, ProfileID: proposal.ProfileID, Model: proposal.Model,
		StructuredOutput: resolved.Profile.StructuredOutput, Temperature: resolved.Profile.Temperature,
		ContextLimit: resolved.Profile.ContextLimit, PromptVersion: proposal.PromptVersion,
		AutoApprovalPolicy: resolved.Profile.AutoApprovalPolicy,
	})
}

func (processor *Processor) buildAutoApproval(profile llm.Profile, request llm.Request,
	proposal llm.Proposal, validation llm.ValidationResult) (*AutoApprovalParams, error) {
	if profile.AutoApprovalPolicy != llm.AutoApprovalGlossaryOnly || len(request.Glossary) == 0 ||
		len(proposal.Changes) == 0 || !autoApprovalValidationPassed(validation) {
		return nil, nil
	}
	for _, change := range proposal.Changes {
		if llm.ApplyGlossary(change.Original, request.Language, request.Glossary) != change.Replacement {
			return nil, nil
		}
	}
	ids := make([]string, 0, 4+len(request.Segments)*2)
	for range 4 + len(request.Segments)*2 {
		id, err := processor.newID()
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return &AutoApprovalParams{
		Policy:   llm.AutoApprovalGlossaryOnly,
		ReviewID: ids[0], AuditID: ids[1], HumanRevisionID: ids[2], ApprovedRevisionID: ids[3],
		HumanSegmentIDs:    ids[4 : 4+len(request.Segments)],
		ApprovedSegmentIDs: ids[4+len(request.Segments):],
	}, nil
}

func autoApprovalValidationPassed(validation llm.ValidationResult) bool {
	return validation.Valid && validation.SchemaValid && validation.OriginalsMatch &&
		validation.GlossarySupported && validation.NumbersPreserved && validation.UnitsPreserved &&
		validation.NegationsPreserved && validation.TimelinePreserved
}

func decodePayload(raw json.RawMessage) (struct{ SourceRevisionID string }, error) {
	var payload struct {
		SourceRevisionID string `json:"source_revision_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return struct{ SourceRevisionID string }{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return struct{ SourceRevisionID string }{}, errors.New("correction payload contains trailing JSON")
	}
	normalized, ok := identifier.NormalizeUUID(payload.SourceRevisionID)
	if !ok {
		return struct{ SourceRevisionID string }{}, errors.New("invalid source revision ID")
	}
	payload.SourceRevisionID = normalized
	return struct{ SourceRevisionID string }{payload.SourceRevisionID}, nil
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

func llmJobErrorCode(err error) string {
	switch llm.ErrorClassOf(err) {
	case llm.ErrorRejected, llm.ErrorUnsafeProposal, llm.ErrorInvalidConfiguration:
		return job.ErrorCodeProviderRejected
	default:
		return job.ErrorCodeProviderUnavailable
	}
}

func (processor *Processor) newID() (string, error) {
	return identifier.NewUUIDFrom(processor.random)
}

func (processor *Processor) fail(ctx context.Context, claimed job.Job, code, stage string) error {
	now := processor.now().UTC()
	_, err := processor.jobs.Fail(ctx, job.FailParams{
		JobID: claimed.ID, WorkerID: processor.workerID, ErrorCode: code,
		Now: now, RetryAt: now.Add(DefaultRetryDelay),
	})
	if err != nil {
		return fmt.Errorf("%w: %s; could not record safe failure", ErrProcessingFailed, stage)
	}
	return fmt.Errorf("%w: %s", ErrProcessingFailed, stage)
}
