package waveform

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	DefaultLeaseDuration = 2 * time.Minute
	DefaultRetryDelay    = 30 * time.Second
	RenderTimeout        = 60 * time.Second
)

var ErrProcessingFailed = errors.New("waveform processing failed")

type JobRepository interface {
	Claim(context.Context, job.ClaimParams) (job.Job, error)
	Fail(context.Context, job.FailParams) (job.Job, error)
}

type AudioSource interface {
	Resolve(context.Context, string, string) (*asr.Audio, error)
}

type Renderer interface {
	Render(context.Context, *asr.Audio) (Rendered, error)
}

type Store interface {
	PutImmutable(context.Context, string, string, string, io.Reader, int64) (storage.Object, error)
}

type Committer interface {
	Commit(context.Context, CommitParams) error
}

type CommitParams struct {
	JobID, WorkerID, WorkspaceID, AssetID, ActorID, AuditID string
	Object                                                  storage.Object
	Width, Height                                           int
	Now                                                     time.Time
}

type Processor struct {
	jobs      JobRepository
	source    AudioSource
	renderer  Renderer
	store     Store
	committer Committer
	workerID  string
	random    io.Reader
	now       func() time.Time
}

func NewProcessor(
	jobs JobRepository,
	source AudioSource,
	renderer Renderer,
	store Store,
	committer Committer,
	workerID string,
) *Processor {
	return &Processor{
		jobs: jobs, source: source, renderer: renderer, store: store,
		committer: committer, workerID: workerID, random: rand.Reader, now: time.Now,
	}
}

func (processor *Processor) RunOnce(ctx context.Context) (bool, error) {
	claimed, err := processor.jobs.Claim(ctx, job.ClaimParams{
		Kind: job.KindGenerateWaveform, WorkerID: processor.workerID,
		Now: processor.now().UTC(), LeaseDuration: DefaultLeaseDuration,
	})
	if errors.Is(err, job.ErrNoClaimableJob) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: claim job", ErrProcessingFailed)
	}
	if processor.source == nil || processor.renderer == nil || processor.store == nil || processor.committer == nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "worker dependencies")
	}

	source, err := processor.source.Resolve(ctx, claimed.WorkspaceID, claimed.AssetID)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInvalidAudio, "load source audio")
	}
	processingContext, cancel := context.WithTimeout(ctx, RenderTimeout)
	defer cancel()
	rendered, err := processor.renderer.Render(processingContext, source)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInvalidAudio, "render waveform")
	}
	defer rendered.Content.Close()
	object, err := processor.store.PutImmutable(
		processingContext, claimed.AssetID, claimed.ID,
		storage.ObjectKindWaveform, rendered.Content, MaxPNGBytes,
	)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "store waveform")
	}
	auditID, err := identifier.NewUUIDFrom(processor.random)
	if err != nil {
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "generate audit identifier")
	}
	err = processor.committer.Commit(ctx, CommitParams{
		JobID: claimed.ID, WorkerID: processor.workerID,
		WorkspaceID: claimed.WorkspaceID, AssetID: claimed.AssetID,
		ActorID: claimed.CreatedBy, AuditID: auditID,
		Object: object, Width: rendered.Width, Height: rendered.Height,
		Now: processor.now().UTC(),
	})
	if err != nil {
		if errors.Is(err, job.ErrLeaseConflict) {
			return true, fmt.Errorf("%w: worker lease was lost", ErrProcessingFailed)
		}
		return true, processor.fail(ctx, claimed, job.ErrorCodeInternal, "commit waveform")
	}
	return true, nil
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
