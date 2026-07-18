// Package transcription coordinates durable jobs, ASR providers, immutable
// raw responses, and transcript persistence.
package transcription

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/hotword"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

const (
	DefaultLeaseDuration = 2 * time.Minute
	DefaultRetryDelay    = 30 * time.Second
	maxRawResponseBytes  = 16 * 1024 * 1024
)

var ErrProcessingFailed = errors.New("transcription processing failed")

type JobRepository interface {
	Claim(ctx context.Context, params job.ClaimParams) (job.Job, error)
	Fail(ctx context.Context, params job.FailParams) (job.Job, error)
}

type AssetRepository interface {
	Get(ctx context.Context, workspaceID, assetID string) (asset.Asset, error)
}

type ProviderResolver interface {
	Resolve(ctx context.Context, workspaceID string) (asr.Transcriber, error)
}

type HotwordResolver interface {
	ResolveForAsset(ctx context.Context, workspaceID, assetID string) (hotword.Resolution, error)
}

type staticProviderResolver struct {
	provider asr.Transcriber
}

func (resolver staticProviderResolver) Resolve(context.Context, string) (asr.Transcriber, error) {
	if resolver.provider == nil {
		return nil, errors.New("ASR provider is unavailable")
	}
	return resolver.provider, nil
}

type RawStore interface {
	PutImmutable(
		ctx context.Context,
		assetID,
		objectID,
		kind string,
		source io.Reader,
		maxBytes int64,
	) (storage.Object, error)
}

type Committer interface {
	CommitRaw(ctx context.Context, params CommitRawParams) (transcript.Revision, error)
}

type CommitRawParams struct {
	JobID                string
	WorkerID             string
	WorkspaceID          string
	AssetID              string
	ActorID              string
	TranscriptID         string
	RevisionID           string
	NormalizedRevisionID string
	RawObjectID          string
	AuditID              string
	Language             string
	Text                 string
	ProviderID           string
	ProviderSnapshot     json.RawMessage
	HotwordSnapshot      json.RawMessage
	RawObject            storage.Object
	Segments             []transcript.Segment
	NormalizedSegments   []transcript.Segment
	Now                  time.Time
}

type Processor struct {
	jobs      JobRepository
	assets    AssetRepository
	audio     AudioSource
	providers ProviderResolver
	hotwords  HotwordResolver
	rawStore  RawStore
	committer Committer
	workerID  string
	random    io.Reader
	now       func() time.Time
}

func NewProcessor(
	jobs JobRepository,
	assets AssetRepository,
	provider asr.Transcriber,
	rawStore RawStore,
	committer Committer,
	workerID string,
) *Processor {
	return NewProcessorWithResolvers(
		jobs, assets, nil, staticProviderResolver{provider: provider}, nil,
		rawStore, committer, workerID,
	)
}

func NewProcessorWithResolver(
	jobs JobRepository,
	assets AssetRepository,
	audioSource AudioSource,
	providers ProviderResolver,
	rawStore RawStore,
	committer Committer,
	workerID string,
) *Processor {
	return NewProcessorWithResolvers(
		jobs, assets, audioSource, providers, nil, rawStore, committer, workerID,
	)
}

func NewProcessorWithResolvers(
	jobs JobRepository,
	assets AssetRepository,
	audioSource AudioSource,
	providers ProviderResolver,
	hotwords HotwordResolver,
	rawStore RawStore,
	committer Committer,
	workerID string,
) *Processor {
	return &Processor{
		jobs: jobs, assets: assets, audio: audioSource, providers: providers, hotwords: hotwords, rawStore: rawStore,
		committer: committer, workerID: workerID, random: rand.Reader, now: time.Now,
	}
}

// RunOnce claims and processes at most one job. processed is false only when
// the queue has no claimable work or claiming itself failed.
func (p *Processor) RunOnce(ctx context.Context) (processed bool, err error) {
	claimed, err := p.jobs.Claim(ctx, job.ClaimParams{
		Kind: job.KindMockTranscribe, WorkerID: p.workerID,
		Now: p.now().UTC(), LeaseDuration: DefaultLeaseDuration,
	})
	if errors.Is(err, job.ErrNoClaimableJob) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: claim job", ErrProcessingFailed)
	}

	source, err := p.assets.Get(ctx, claimed.WorkspaceID, claimed.AssetID)
	if err != nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeInternal, "load asset")
	}
	durationMS := int64(0)
	if source.DurationMS != nil {
		durationMS = *source.DurationMS
	}
	if p.providers == nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeProviderUnavailable, "resolve provider route")
	}
	provider, err := p.providers.Resolve(ctx, claimed.WorkspaceID)
	if err != nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeProviderUnavailable, "resolve provider route")
	}
	var sourceAudio *asr.Audio
	if p.audio != nil {
		sourceAudio, err = p.audio.Resolve(ctx, claimed.WorkspaceID, claimed.AssetID)
		if err != nil {
			return true, p.fail(ctx, claimed, job.ErrorCodeInvalidAudio, "load source audio")
		}
	}
	hotwordResolution := hotword.Resolution{Snapshot: json.RawMessage(`{}`)}
	if p.hotwords != nil {
		hotwordResolution, err = p.hotwords.ResolveForAsset(
			ctx, claimed.WorkspaceID, claimed.AssetID,
		)
		if err != nil {
			return true, p.fail(ctx, claimed, job.ErrorCodeInternal, "resolve hotwords")
		}
	}
	result, err := provider.Transcribe(ctx, asr.Input{
		AssetID: source.ID, Language: source.Language, DurationMS: durationMS,
		Audio: sourceAudio, Hotwords: hotwordResolution.Hotwords,
	})
	if err != nil {
		return true, p.fail(ctx, claimed, providerJobErrorCode(err), "provider unavailable")
	}
	if err := result.Validate(); err != nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeProviderRejected, "provider returned an invalid result")
	}
	rawObject, err := p.rawStore.PutImmutable(
		ctx,
		claimed.AssetID,
		claimed.ID,
		storage.ObjectKindProviderRawResponse,
		bytes.NewReader(result.RawJSON),
		maxRawResponseBytes,
	)
	if err != nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeInternal, "store raw response")
	}
	segments, err := p.convertSegments(result.Segments)
	if err != nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeInternal, "prepare transcript timeline")
	}
	normalizedRevisionID, err := identifier.NewUUIDFrom(p.random)
	if err != nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeInternal, "generate normalized revision identifier")
	}
	normalizedSegments, err := p.cloneSegmentsWithNewIDs(segments)
	if err != nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeInternal, "prepare normalized timeline")
	}
	auditID, err := identifier.NewUUIDFrom(p.random)
	if err != nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeInternal, "generate audit identifier")
	}
	providerSnapshot, err := buildProviderSnapshot(result)
	if err != nil {
		return true, p.fail(ctx, claimed, job.ErrorCodeInternal, "prepare provider snapshot")
	}
	_, err = p.committer.CommitRaw(ctx, CommitRawParams{
		JobID: claimed.ID, WorkerID: p.workerID,
		WorkspaceID: claimed.WorkspaceID, AssetID: claimed.AssetID, ActorID: claimed.CreatedBy,
		TranscriptID: claimed.AssetID, RevisionID: claimed.ID,
		NormalizedRevisionID: normalizedRevisionID, RawObjectID: claimed.ID,
		AuditID: auditID, Language: result.Language, Text: result.Text,
		ProviderID: normalizedProviderID(result), ProviderSnapshot: providerSnapshot,
		HotwordSnapshot: hotwordResolution.Snapshot,
		RawObject:       rawObject, Segments: segments,
		NormalizedSegments: normalizedSegments, Now: p.now().UTC(),
	})
	if err != nil {
		if errors.Is(err, job.ErrLeaseConflict) {
			return true, fmt.Errorf("%w: worker lease was lost", ErrProcessingFailed)
		}
		return true, p.fail(ctx, claimed, job.ErrorCodeInternal, "commit transcript")
	}
	return true, nil
}

func (p *Processor) cloneSegmentsWithNewIDs(input []transcript.Segment) ([]transcript.Segment, error) {
	result := cloneSegments(input)
	for index := range result {
		segmentID, err := identifier.NewUUIDFrom(p.random)
		if err != nil {
			return nil, err
		}
		result[index].ID = segmentID
	}
	return result, nil
}

func buildProviderSnapshot(result asr.Result) (json.RawMessage, error) {
	providerID := normalizedProviderID(result)
	return json.Marshal(struct {
		ProviderID string `json:"provider_id"`
		ProfileID  string `json:"profile_id,omitempty"`
		RawSchema  string `json:"raw_schema"`
		Version    string `json:"version"`
	}{
		ProviderID: providerID, ProfileID: result.ProfileID,
		RawSchema: result.RawSchema, Version: "1",
	})
}

func normalizedProviderID(result asr.Result) string {
	if result.ProviderID == "" {
		return "unknown"
	}
	return result.ProviderID
}

func providerJobErrorCode(err error) string {
	switch asr.ErrorClassOf(err) {
	case asr.ErrorInvalidAudio:
		return job.ErrorCodeInvalidAudio
	case asr.ErrorRejected, asr.ErrorUnsupported, asr.ErrorInvalidConfiguration:
		return job.ErrorCodeProviderRejected
	default:
		return job.ErrorCodeProviderUnavailable
	}
}

func (p *Processor) convertSegments(input []asr.Segment) ([]transcript.Segment, error) {
	segments := make([]transcript.Segment, 0, len(input))
	for ordinal, source := range input {
		segmentID, err := identifier.NewUUIDFrom(p.random)
		if err != nil {
			return nil, err
		}
		words, err := json.Marshal(source.Words)
		if err != nil {
			return nil, fmt.Errorf("encode words: %w", err)
		}
		var speaker *string
		if source.Speaker != "" {
			value := source.Speaker
			speaker = &value
		}
		var confidence *float64
		if source.Confidence != nil {
			value := *source.Confidence
			confidence = &value
		}
		segments = append(segments, transcript.Segment{
			ID: segmentID, Ordinal: ordinal, StartMS: source.StartMS, EndMS: source.EndMS,
			Speaker: speaker, Text: source.Text, Confidence: confidence, Words: words,
		})
	}
	return segments, nil
}

func (p *Processor) fail(ctx context.Context, claimed job.Job, code, stage string) error {
	now := p.now().UTC()
	_, err := p.jobs.Fail(ctx, job.FailParams{
		JobID: claimed.ID, WorkerID: p.workerID, ErrorCode: code,
		Now: now, RetryAt: now.Add(DefaultRetryDelay),
	})
	if err != nil {
		return fmt.Errorf("%w: %s; could not record safe failure", ErrProcessingFailed, stage)
	}
	return fmt.Errorf("%w: %s", ErrProcessingFailed, stage)
}
