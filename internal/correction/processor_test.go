package correction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/glossary"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/llmprofile"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

const (
	testJobID      = "11111111-1111-4111-8111-111111111111"
	testRevisionID = "22222222-2222-4222-8222-222222222222"
	testSegmentID  = "33333333-3333-4333-8333-333333333333"
)

type fakeJobs struct {
	claimed    job.Job
	claimErr   error
	failParams job.FailParams
}

func (repository *fakeJobs) Claim(context.Context, job.ClaimParams) (job.Job, error) {
	return repository.claimed, repository.claimErr
}
func (repository *fakeJobs) Fail(_ context.Context, params job.FailParams) (job.Job, error) {
	repository.failParams = params
	return job.Job{}, nil
}

type fakeRevisions struct{ source transcript.Revision }

func (repository fakeRevisions) GetRevision(context.Context, string, string) (transcript.Revision, error) {
	return repository.source, nil
}

type fakeProviderResolver struct{ resolved llmprofile.ResolvedProvider }

func (resolver fakeProviderResolver) Resolve(context.Context, string) (llmprofile.ResolvedProvider, error) {
	return resolver.resolved, nil
}

type fakeGlossaries struct{ result glossary.Resolution }

func (resolver fakeGlossaries) ResolveForAssetWithDefault(context.Context, string, string, string) (glossary.Resolution, error) {
	return resolver.result, nil
}

type fakeRawStore struct{ raw []byte }

func (store *fakeRawStore) PutImmutable(_ context.Context, _, _, _ string, source io.Reader, _ int64) (storage.Object, error) {
	value, err := io.ReadAll(source)
	if err != nil {
		return storage.Object{}, err
	}
	store.raw = value
	digest := sha256.Sum256(value)
	return storage.Object{
		Backend: storage.BackendLocal,
		Key:     "objects/fixture", Size: int64(len(value)), SHA256: hex.EncodeToString(digest[:]),
	}, nil
}

type fakeCommitter struct {
	params CommitParams
	calls  int
}

func (committer *fakeCommitter) Commit(_ context.Context, params CommitParams) (transcript.Revision, error) {
	committer.calls++
	committer.params = params
	return transcript.Revision{ID: params.RevisionID}, nil
}

func sourceRevision() transcript.Revision {
	confidence := 0.91
	return transcript.Revision{
		ID: testRevisionID, TranscriptID: "44444444-4444-4444-8444-444444444444",
		AssetID: "55555555-5555-4555-8555-555555555555", Kind: transcript.KindRawASR,
		Language: "zh-CN", Text: "今天讨论容易云调度平台的版本 2.0。", HotwordSnapshot: json.RawMessage(`{"sets":[]}`),
		Segments: []transcript.Segment{{
			ID: testSegmentID, Ordinal: 0, StartMS: 100, EndMS: 2400,
			Text: "今天讨论容易云调度平台的版本 2.0。", Confidence: &confidence,
			Words: json.RawMessage(`[{"text":"容易云"}]`),
		}},
	}
}

func configuredProcessor(t *testing.T) (*Processor, *fakeJobs, *fakeRawStore, *fakeCommitter) {
	t.Helper()
	profile := llm.DefaultMockProfile("mock-profile")
	provider, err := llm.NewMockProvider(profile)
	if err != nil {
		t.Fatal(err)
	}
	source := sourceRevision()
	payload, _ := json.Marshal(struct {
		SourceRevisionID string `json:"source_revision_id"`
	}{source.ID})
	jobs := &fakeJobs{claimed: job.Job{
		ID: testJobID, WorkspaceID: "workspace", AssetID: source.AssetID,
		CreatedBy: "user", Kind: job.KindLLMCorrect, State: job.StateRunning, Payload: payload,
	}}
	rawStore := &fakeRawStore{}
	committer := &fakeCommitter{}
	processor := NewProcessor(
		jobs, fakeRevisions{source}, fakeProviderResolver{llmprofile.ResolvedProvider{Provider: provider, Profile: profile}},
		fakeGlossaries{glossary.Resolution{
			Rules: []llm.GlossaryRule{{
				CanonicalForm: "容器云", Aliases: []string{"容易云"}, Language: "zh-CN",
				ContextTerms: []string{"调度"}, Priority: 100,
			}}, Snapshot: []byte(`{"sets":[{"id":"fixture","version":1}]}`),
		}}, rawStore, committer, "worker",
	)
	processor.random = zeroReader{}
	processor.now = func() time.Time { return time.Date(2026, 7, 16, 14, 0, 0, 0, time.UTC) }
	return processor, jobs, rawStore, committer
}

type zeroReader struct{}

func (zeroReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = 0
	}
	return len(value), nil
}

type sequentialReader struct{ next byte }

func (reader *sequentialReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = reader.next
		reader.next++
	}
	return len(value), nil
}

func TestProcessorCreatesValidatedImmutableCorrection(t *testing.T) {
	processor, jobs, rawStore, committer := configuredProcessor(t)
	processed, err := processor.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = %t, %v", processed, err)
	}
	if committer.calls != 1 || jobs.failParams.JobID != "" {
		t.Fatalf("commit calls = %d; failure = %#v", committer.calls, jobs.failParams)
	}
	params := committer.params
	if params.SourceRevisionID != testRevisionID || params.Text != "今天讨论容器云调度平台的版本 2.0。" ||
		params.ProviderID != llm.MockProviderID || params.PromptVersion != llm.PromptVersionV1 ||
		params.RawObject.Backend != storage.BackendLocal {
		t.Fatalf("commit params = %#v", params)
	}
	if len(params.Segments) != 1 || params.Segments[0].StartMS != 100 || params.Segments[0].EndMS != 2400 ||
		params.Segments[0].Text != "今天讨论容器云调度平台的版本 2.0。" || string(params.Segments[0].Words) != "[]" {
		t.Fatalf("corrected segments = %#v", params.Segments)
	}
	if !json.Valid(rawStore.raw) || !json.Valid(params.Diff) || !json.Valid(params.ValidationResult) {
		t.Fatal("raw response, diff, and validation result must be valid JSON")
	}
	var validation llm.ValidationResult
	if err := json.Unmarshal(params.ValidationResult, &validation); err != nil || !validation.Valid || !validation.TimelinePreserved {
		t.Fatalf("validation = %#v, %v", validation, err)
	}
}

func TestProcessorRequestsAutoApprovalOnlyForValidatedGlossaryChanges(t *testing.T) {
	processor, jobs, _, committer := configuredProcessor(t)
	profile := llm.DefaultMockProfile("mock-profile")
	profile.AutoApprovalPolicy = "validated_glossary_only"
	provider, err := llm.NewMockProvider(profile)
	if err != nil {
		t.Fatal(err)
	}
	processor.providers = fakeProviderResolver{llmprofile.ResolvedProvider{Provider: provider, Profile: profile}}
	processor.random = &sequentialReader{}

	processed, err := processor.RunOnce(context.Background())
	if err != nil || !processed || jobs.failParams.JobID != "" {
		t.Fatalf("RunOnce() = %t, %v; failure = %#v", processed, err, jobs.failParams)
	}
	auto := committer.params.AutoApproval
	if auto == nil || auto.Policy != "validated_glossary_only" ||
		len(auto.HumanSegmentIDs) != len(committer.params.Segments) ||
		len(auto.ApprovedSegmentIDs) != len(committer.params.Segments) {
		t.Fatalf("auto approval = %#v", auto)
	}
	ids := []string{auto.ReviewID, auto.AuditID, auto.HumanRevisionID, auto.ApprovedRevisionID}
	ids = append(ids, auto.HumanSegmentIDs...)
	ids = append(ids, auto.ApprovedSegmentIDs...)
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			t.Fatal("auto approval contains an empty identifier")
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("auto approval contains duplicate identifier %q", id)
		}
		seen[id] = struct{}{}
	}
	var snapshot struct {
		AutoApprovalPolicy string `json:"auto_approval_policy"`
	}
	if err := json.Unmarshal(committer.params.ProviderSnapshot, &snapshot); err != nil ||
		snapshot.AutoApprovalPolicy != "validated_glossary_only" {
		t.Fatalf("provider snapshot = %s, %v", committer.params.ProviderSnapshot, err)
	}
}

func TestProcessorKeepsEmptyValidatedGlossaryProposalPending(t *testing.T) {
	processor, _, _, committer := configuredProcessor(t)
	profile := llm.DefaultMockProfile("mock-profile")
	profile.AutoApprovalPolicy = "validated_glossary_only"
	provider, err := llm.NewMockProvider(profile)
	if err != nil {
		t.Fatal(err)
	}
	processor.providers = fakeProviderResolver{llmprofile.ResolvedProvider{Provider: provider, Profile: profile}}
	processor.glossaries = fakeGlossaries{glossary.Resolution{
		Rules: nil, Snapshot: []byte(`{"sets":[]}`),
	}}
	processor.random = &sequentialReader{}
	processed, err := processor.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = %t, %v", processed, err)
	}
	if committer.params.AutoApproval != nil {
		t.Fatalf("empty proposal auto approval = %#v", committer.params.AutoApproval)
	}
}

func TestProcessorTreatsInvalidPayloadAsSafeFailure(t *testing.T) {
	processor, jobs, _, committer := configuredProcessor(t)
	jobs.claimed.Payload = json.RawMessage(`{"source_revision_id":"bad","extra":true}`)
	processed, err := processor.RunOnce(context.Background())
	if !processed || !errors.Is(err, ErrProcessingFailed) {
		t.Fatalf("RunOnce() = %t, %v", processed, err)
	}
	if committer.calls != 0 || jobs.failParams.ErrorCode != job.ErrorCodeInternal {
		t.Fatalf("commit calls = %d; failure = %#v", committer.calls, jobs.failParams)
	}
}

func TestProcessorIsIdleWhenNoCorrectionIsClaimable(t *testing.T) {
	processor, jobs, _, _ := configuredProcessor(t)
	jobs.claimErr = job.ErrNoClaimableJob
	processed, err := processor.RunOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("RunOnce() = %t, %v", processed, err)
	}
}
