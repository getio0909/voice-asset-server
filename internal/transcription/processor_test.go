package transcription

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/hotword"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

func TestProcessorRunsMockASRAndCommitsImmutableResult(t *testing.T) {
	now := time.Date(2026, 7, 16, 6, 30, 0, 0, time.UTC)
	duration := int64(4_000)
	jobs := &fakeJobRepository{claimed: job.Job{
		ID:          "70000000-0000-4000-8000-000000000061",
		WorkspaceID: "10000000-0000-4000-8000-000000000061",
		AssetID:     "30000000-0000-4000-8000-000000000061",
		CreatedBy:   "20000000-0000-4000-8000-000000000061",
		Kind:        job.KindMockTranscribe, State: job.StateRunning, Attempts: 1, MaxAttempts: 3,
	}}
	assets := &fakeAssetRepository{asset: asset.Asset{
		ID: jobs.claimed.AssetID, WorkspaceID: jobs.claimed.WorkspaceID,
		Language: "zh-CN", DurationMS: &duration, Status: "processing",
	}}
	providerResult, err := asr.NewMockProvider().Transcribe(context.Background(), asr.Input{Language: "zh-CN"})
	if err != nil {
		t.Fatalf("create mock ASR fixture: %v", err)
	}
	provider := &fakeProvider{result: providerResult}
	store := &fakeRawStore{object: storage.Object{
		Backend: storage.BackendLocal, Key: "objects/raw/result.json", Size: 128,
		SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}
	committer := &fakeCommitter{revision: transcript.Revision{ID: jobs.claimed.ID}}
	processor := NewProcessor(jobs, assets, provider, store, committer, "worker-1")
	processor.now = func() time.Time { return now }
	processor.random = &sequentialReader{}

	processed, err := processor.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = (%t, %v)", processed, err)
	}
	if jobs.claimParams.Kind != job.KindMockTranscribe || jobs.claimParams.WorkerID != "worker-1" ||
		jobs.claimParams.LeaseDuration != DefaultLeaseDuration {
		t.Fatalf("claim params = %+v", jobs.claimParams)
	}
	if provider.input.AssetID != jobs.claimed.AssetID || provider.input.Language != "zh-CN" ||
		provider.input.DurationMS != duration {
		t.Fatalf("provider input = %+v", provider.input)
	}
	if store.assetID != jobs.claimed.AssetID || store.objectID != jobs.claimed.ID ||
		store.kind != storage.ObjectKindProviderRawResponse || !bytes.Equal(store.content, provider.result.RawJSON) {
		t.Fatalf("raw store call = %q/%q/%q/%q", store.assetID, store.objectID, store.kind, store.content)
	}
	params := committer.params
	if params.JobID != jobs.claimed.ID || params.RevisionID != jobs.claimed.ID ||
		params.NormalizedRevisionID == "" || params.RawObjectID != jobs.claimed.ID ||
		params.TranscriptID != jobs.claimed.AssetID ||
		params.RawObject.Backend != storage.BackendLocal ||
		params.Language != "zh-CN" || params.Text != provider.result.Text ||
		params.ProviderID != asr.MockProviderID || string(params.HotwordSnapshot) != "{}" ||
		len(params.Segments) != 2 || len(params.NormalizedSegments) != 2 {
		t.Fatalf("commit params = %+v", params)
	}
	if params.Segments[0].Ordinal != 0 || params.Segments[0].StartMS != 0 ||
		params.Segments[0].EndMS != 450 || len(params.Segments[0].Words) == 0 {
		t.Fatalf("committed segment = %+v", params.Segments[0])
	}
	if params.NormalizedSegments[0].ID == params.Segments[0].ID ||
		params.NormalizedSegments[0].Text != params.Segments[0].Text {
		t.Fatalf("normalized segment = %+v", params.NormalizedSegments[0])
	}
	if jobs.failCalls != 0 {
		t.Fatalf("Fail() calls = %d", jobs.failCalls)
	}
}

func TestProcessorReturnsIdleWhenNoJobIsClaimable(t *testing.T) {
	jobs := &fakeJobRepository{claimErr: job.ErrNoClaimableJob}
	processor := NewProcessor(jobs, &fakeAssetRepository{}, &fakeProvider{}, &fakeRawStore{}, &fakeCommitter{}, "worker-1")
	processed, err := processor.RunOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("RunOnce() = (%t, %v), want idle", processed, err)
	}
}

func TestProcessorFailsClaimWithSafeCodeAndDoesNotReturnProviderDetails(t *testing.T) {
	now := time.Date(2026, 7, 16, 6, 30, 0, 0, time.UTC)
	jobs := &fakeJobRepository{claimed: job.Job{
		ID: "job-1", WorkspaceID: "workspace-1", AssetID: "asset-1",
		Kind: job.KindMockTranscribe, State: job.StateRunning,
	}}
	provider := &fakeProvider{err: errors.New("vendor-secret-response")}
	processor := NewProcessor(
		jobs,
		&fakeAssetRepository{asset: asset.Asset{ID: "asset-1", WorkspaceID: "workspace-1", Language: "en-US"}},
		provider,
		&fakeRawStore{},
		&fakeCommitter{},
		"worker-1",
	)
	processor.now = func() time.Time { return now }

	processed, err := processor.RunOnce(context.Background())
	if !processed || !errors.Is(err, ErrProcessingFailed) {
		t.Fatalf("RunOnce() = (%t, %v)", processed, err)
	}
	if strings.Contains(err.Error(), "vendor-secret-response") {
		t.Fatalf("RunOnce() leaked provider error: %v", err)
	}
	if jobs.failCalls != 1 || jobs.failParams.ErrorCode != job.ErrorCodeProviderUnavailable ||
		!jobs.failParams.RetryAt.Equal(now.Add(DefaultRetryDelay)) {
		t.Fatalf("Fail() = %d/%+v", jobs.failCalls, jobs.failParams)
	}
}

func TestProcessorResolvesWorkspaceProviderAndCommitsDynamicSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	duration := int64(1_000)
	jobs := &fakeJobRepository{claimed: job.Job{
		ID: "job-1", WorkspaceID: "workspace-1", AssetID: "asset-1", CreatedBy: "user-1",
		Kind: job.KindMockTranscribe, State: job.StateRunning,
	}}
	assets := &fakeAssetRepository{asset: asset.Asset{
		ID: "asset-1", WorkspaceID: "workspace-1", Language: "zh-CN", DurationMS: &duration,
	}}
	result, err := asr.NewMockProvider().Transcribe(context.Background(), asr.Input{Language: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	result.ProviderID = asr.TencentProviderID
	result.ProfileID = "profile-1"
	result.RawSchema = asr.RawSchemaTencentFlashV1
	provider := &fakeProvider{result: result}
	resolver := &fakeProviderResolver{provider: provider}
	audioSource := &fakeProcessorAudioSource{audio: &asr.Audio{
		SizeBytes: 5, Format: "m4a", SampleRate: 16_000,
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("audio")), nil
		},
	}}
	committer := &fakeCommitter{}
	hotwordResolver := &fakeHotwordResolver{resolution: hotword.Resolution{
		Hotwords: []asr.Hotword{{Term: "VoiceAsset", Weight: 90}},
		Snapshot: json.RawMessage(`{"sets":[{"id":"set-1","version":3}]}`),
	}}
	processor := NewProcessorWithResolvers(
		jobs, assets, audioSource, resolver, hotwordResolver,
		&fakeRawStore{}, committer, "worker-1",
	)
	processor.now = func() time.Time { return now }
	processor.random = &sequentialReader{}
	processed, err := processor.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = (%t, %v)", processed, err)
	}
	if resolver.workspaceID != "workspace-1" || audioSource.workspaceID != "workspace-1" ||
		provider.input.Audio != audioSource.audio || hotwordResolver.workspaceID != "workspace-1" ||
		hotwordResolver.assetID != "asset-1" ||
		!reflect.DeepEqual(provider.input.Hotwords, hotwordResolver.resolution.Hotwords) {
		t.Fatal("processor did not route workspace audio and provider together")
	}
	var snapshot struct {
		ProviderID string `json:"provider_id"`
		ProfileID  string `json:"profile_id"`
		RawSchema  string `json:"raw_schema"`
	}
	if err := json.Unmarshal(committer.params.ProviderSnapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ProviderID != asr.TencentProviderID || snapshot.ProfileID != "profile-1" ||
		snapshot.RawSchema != asr.RawSchemaTencentFlashV1 {
		t.Fatalf("provider snapshot = %+v", snapshot)
	}
	if committer.params.ProviderID != asr.TencentProviderID ||
		string(committer.params.HotwordSnapshot) != string(hotwordResolver.resolution.Snapshot) {
		t.Fatalf("provider commit metadata = %q/%s", committer.params.ProviderID, committer.params.HotwordSnapshot)
	}
}

type fakeJobRepository struct {
	claimed     job.Job
	claimErr    error
	claimParams job.ClaimParams
	failParams  job.FailParams
	failCalls   int
	failErr     error
}

type sequentialReader struct {
	next byte
}

func (r *sequentialReader) Read(p []byte) (int, error) {
	for index := range p {
		p[index] = r.next
		r.next++
	}
	return len(p), nil
}

func (r *fakeJobRepository) Claim(_ context.Context, params job.ClaimParams) (job.Job, error) {
	r.claimParams = params
	return r.claimed, r.claimErr
}

func (r *fakeJobRepository) Fail(_ context.Context, params job.FailParams) (job.Job, error) {
	r.failCalls++
	r.failParams = params
	return r.claimed, r.failErr
}

type fakeAssetRepository struct {
	asset asset.Asset
	err   error
}

func (r *fakeAssetRepository) Get(_ context.Context, _, _ string) (asset.Asset, error) {
	return r.asset, r.err
}

type fakeProvider struct {
	result asr.Result
	err    error
	input  asr.Input
}

type fakeProviderResolver struct {
	provider    asr.Transcriber
	err         error
	workspaceID string
}

func (resolver *fakeProviderResolver) Resolve(_ context.Context, workspaceID string) (asr.Transcriber, error) {
	resolver.workspaceID = workspaceID
	return resolver.provider, resolver.err
}

type fakeProcessorAudioSource struct {
	audio       *asr.Audio
	err         error
	workspaceID string
	assetID     string
}

type fakeHotwordResolver struct {
	resolution  hotword.Resolution
	err         error
	workspaceID string
	assetID     string
}

func (resolver *fakeHotwordResolver) ResolveForAsset(
	_ context.Context,
	workspaceID,
	assetID string,
) (hotword.Resolution, error) {
	resolver.workspaceID = workspaceID
	resolver.assetID = assetID
	return resolver.resolution, resolver.err
}

func (source *fakeProcessorAudioSource) Resolve(
	_ context.Context,
	workspaceID,
	assetID string,
) (*asr.Audio, error) {
	source.workspaceID = workspaceID
	source.assetID = assetID
	return source.audio, source.err
}

func (p *fakeProvider) Transcribe(_ context.Context, input asr.Input) (asr.Result, error) {
	p.input = input
	return p.result, p.err
}

type fakeRawStore struct {
	object   storage.Object
	err      error
	assetID  string
	objectID string
	kind     string
	content  []byte
}

func (s *fakeRawStore) PutImmutable(
	_ context.Context,
	assetID,
	objectID,
	kind string,
	source io.Reader,
	_ int64,
) (storage.Object, error) {
	s.assetID = assetID
	s.objectID = objectID
	s.kind = kind
	s.content, _ = io.ReadAll(source)
	return s.object, s.err
}

type fakeCommitter struct {
	params   CommitRawParams
	revision transcript.Revision
	err      error
}

func (c *fakeCommitter) CommitRaw(_ context.Context, params CommitRawParams) (transcript.Revision, error) {
	c.params = params
	return c.revision, c.err
}
