package waveform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

func TestProcessorRendersStoresAndCommitsOneWaveform(t *testing.T) {
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	claimed := job.Job{
		ID: "job-1", WorkspaceID: "workspace-1", AssetID: "asset-1", CreatedBy: "user-1",
		Kind: job.KindGenerateWaveform, State: job.StateRunning,
	}
	jobs := &fakeWaveformJobs{claimed: claimed}
	source := &fakeWaveformSource{audio: &asr.Audio{SizeBytes: 10, Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("0123456789")), nil
	}}}
	renderer := &fakeWaveformRenderer{rendered: Rendered{
		Content: io.NopCloser(strings.NewReader("png-content")), Width: Width, Height: Height,
	}}
	store := &fakeWaveformStore{object: storage.Object{
		Backend: storage.BackendLocal, Key: "derived/waveform.png", Size: 11, SHA256: strings.Repeat("a", 64),
	}}
	committer := &fakeWaveformCommitter{}
	processor := NewProcessor(jobs, source, renderer, store, committer, "worker-1")
	processor.random = bytes.NewReader(bytes.Repeat([]byte{0x07}, 16))
	processor.now = func() time.Time { return now }

	processed, err := processor.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = (%t, %v)", processed, err)
	}
	if jobs.claimParams.Kind != job.KindGenerateWaveform || store.kind != storage.ObjectKindWaveform ||
		store.maxBytes != MaxPNGBytes || store.assetID != claimed.AssetID || store.objectID != claimed.ID {
		t.Fatalf("claim/store = %+v/%q/%q/%q/%d", jobs.claimParams, store.kind, store.assetID, store.objectID, store.maxBytes)
	}
	if committer.params.JobID != claimed.ID || committer.params.AuditID == "" ||
		committer.params.Object.Backend != storage.BackendLocal ||
		committer.params.Width != Width || committer.params.Height != Height || jobs.failCalls != 0 {
		t.Fatalf("commit/failure = %+v/%d", committer.params, jobs.failCalls)
	}
}

func TestProcessorRecordsSafeInvalidAudioFailure(t *testing.T) {
	claimed := job.Job{ID: "job-1", WorkspaceID: "workspace-1", AssetID: "asset-1", CreatedBy: "user-1"}
	jobs := &fakeWaveformJobs{claimed: claimed}
	processor := NewProcessor(
		jobs,
		&fakeWaveformSource{audio: &asr.Audio{SizeBytes: 1}},
		&fakeWaveformRenderer{err: ErrRender},
		&fakeWaveformStore{}, &fakeWaveformCommitter{}, "worker-1",
	)
	processor.now = func() time.Time { return time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC) }

	processed, err := processor.RunOnce(context.Background())
	if !processed || !errors.Is(err, ErrProcessingFailed) || jobs.failParams.ErrorCode != job.ErrorCodeInvalidAudio {
		t.Fatalf("RunOnce() = (%t, %v), failure = %+v", processed, err, jobs.failParams)
	}
}

func TestProcessorIsIdleWhenNoWaveformJobExists(t *testing.T) {
	jobs := &fakeWaveformJobs{claimErr: job.ErrNoClaimableJob}
	processed, err := NewProcessor(jobs, nil, nil, nil, nil, "worker-1").RunOnce(context.Background())
	if processed || err != nil || jobs.failCalls != 0 {
		t.Fatalf("RunOnce() = (%t, %v), failures = %d", processed, err, jobs.failCalls)
	}
}

type fakeWaveformJobs struct {
	claimed     job.Job
	claimErr    error
	claimParams job.ClaimParams
	failParams  job.FailParams
	failCalls   int
}

func (repository *fakeWaveformJobs) Claim(_ context.Context, params job.ClaimParams) (job.Job, error) {
	repository.claimParams = params
	return repository.claimed, repository.claimErr
}

func (repository *fakeWaveformJobs) Fail(_ context.Context, params job.FailParams) (job.Job, error) {
	repository.failCalls++
	repository.failParams = params
	return repository.claimed, nil
}

type fakeWaveformSource struct {
	audio *asr.Audio
	err   error
}

func (source *fakeWaveformSource) Resolve(context.Context, string, string) (*asr.Audio, error) {
	return source.audio, source.err
}

type fakeWaveformRenderer struct {
	rendered Rendered
	err      error
}

func (renderer *fakeWaveformRenderer) Render(context.Context, *asr.Audio) (Rendered, error) {
	return renderer.rendered, renderer.err
}

type fakeWaveformStore struct {
	object                  storage.Object
	assetID, objectID, kind string
	maxBytes                int64
}

func (store *fakeWaveformStore) PutImmutable(
	_ context.Context, assetID, objectID, kind string, _ io.Reader, maxBytes int64,
) (storage.Object, error) {
	store.assetID, store.objectID, store.kind, store.maxBytes = assetID, objectID, kind, maxBytes
	return store.object, nil
}

type fakeWaveformCommitter struct {
	params CommitParams
	err    error
}

func (committer *fakeWaveformCommitter) Commit(_ context.Context, params CommitParams) error {
	committer.params = params
	return committer.err
}
