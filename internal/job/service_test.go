package job

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestServiceCreateTranscriptionRequiresScope(t *testing.T) {
	repository := &stubRepository{}
	service := NewService(repository)

	_, _, err := service.CreateTranscription(context.Background(), auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsRead},
	}, "asset-1", "request-1")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateTranscription() error = %v, want ErrForbidden", err)
	}
	if repository.createCalls != 0 {
		t.Fatalf("repository create calls = %d, want 0", repository.createCalls)
	}
}

func TestServiceCreateTranscriptionBuildsWorkspaceScopedRequest(t *testing.T) {
	repository := &stubRepository{created: Job{ID: "job-1", State: StateQueued}}
	service := NewService(repository)
	service.random = bytes.NewReader(make([]byte, 32))
	principal := auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1",
		Scopes: []string{auth.ScopeTranscriptionsWrite},
	}

	created, replayed, err := service.CreateTranscription(
		context.Background(), principal, "  asset-1  ", "  request-1  ",
	)
	if err != nil {
		t.Fatalf("CreateTranscription() error = %v", err)
	}
	if replayed || created.ID != "job-1" {
		t.Fatalf("CreateTranscription() = (%+v, %t), want new job-1", created, replayed)
	}
	params := repository.params
	if params.WorkspaceID != principal.WorkspaceID || params.CreatedBy != principal.UserID {
		t.Fatalf("repository scope = %q/%q", params.WorkspaceID, params.CreatedBy)
	}
	if params.AssetID != "asset-1" || params.IdempotencyKey != "request-1" {
		t.Fatalf("repository request = asset %q / key %q", params.AssetID, params.IdempotencyKey)
	}
	if params.Kind != KindMockTranscribe || params.RequestHash == "" {
		t.Fatalf("repository kind/hash = %q/%q", params.Kind, params.RequestHash)
	}
}

func TestServiceCreateTranscriptionRejectsInvalidInput(t *testing.T) {
	service := NewService(&stubRepository{})
	principal := auth.Principal{Scopes: []string{auth.ScopeTranscriptionsWrite}}

	for _, testCase := range []struct {
		name    string
		assetID string
		key     string
	}{
		{name: "missing asset", key: "request-1"},
		{name: "missing key", assetID: "asset-1"},
		{name: "control character", assetID: "asset-1", key: "bad\nkey"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := service.CreateTranscription(
				context.Background(), principal, testCase.assetID, testCase.key,
			)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("CreateTranscription() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestServiceCreateCorrectionBuildsImmutableRevisionRequest(t *testing.T) {
	repository := &stubRepository{correctionCreated: Job{ID: "job-2", Kind: KindLLMCorrect}}
	service := NewService(repository)
	service.random = bytes.NewReader(make([]byte, 32))
	principal := auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeCorrectionsWrite},
	}
	revisionID := "11111111-1111-4111-8111-111111111111"
	created, replayed, err := service.CreateCorrection(context.Background(), principal, revisionID, "correct-1")
	if err != nil || replayed || created.ID != "job-2" {
		t.Fatalf("CreateCorrection() = (%+v, %t, %v)", created, replayed, err)
	}
	params := repository.correctionParams
	if params.SourceRevisionID != revisionID || params.WorkspaceID != principal.WorkspaceID ||
		params.Kind != KindLLMCorrect || params.RequestHash == "" {
		t.Fatalf("correction params = %#v", params)
	}
	if _, _, err := service.CreateCorrection(context.Background(), auth.Principal{
		Scopes: []string{auth.ScopeTranscriptsRead},
	}, revisionID, "correct-2"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing scope error = %v", err)
	}
}

func TestServiceGetRequiresReadScopeAndUsesPrincipalWorkspace(t *testing.T) {
	repository := &stubRepository{got: Job{ID: "job-1", WorkspaceID: "workspace-1"}}
	service := NewService(repository)
	if _, err := service.Get(context.Background(), auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptionsWrite},
	}, "job-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Get() without read scope error = %v, want ErrForbidden", err)
	}

	result, err := service.Get(context.Background(), auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptsRead},
	}, " job-1 ")
	if err != nil || result.ID != "job-1" {
		t.Fatalf("Get() = (%+v, %v)", result, err)
	}
	if repository.getWorkspaceID != "workspace-1" || repository.getJobID != "job-1" {
		t.Fatalf("repository Get() scope = %q/%q", repository.getWorkspaceID, repository.getJobID)
	}
}

type stubRepository struct {
	createCalls        int
	params             CreateTranscriptionParams
	created            Job
	replayed           bool
	createErr          error
	got                Job
	getErr             error
	getWorkspaceID     string
	getJobID           string
	correctionParams   CreateCorrectionParams
	correctionCreated  Job
	correctionReplayed bool
	correctionErr      error
}

func (r *stubRepository) CreateCorrection(
	_ context.Context,
	params CreateCorrectionParams,
) (Job, bool, error) {
	r.correctionParams = params
	return r.correctionCreated, r.correctionReplayed, r.correctionErr
}

func (r *stubRepository) CreateTranscription(
	_ context.Context,
	params CreateTranscriptionParams,
) (Job, bool, error) {
	r.createCalls++
	r.params = params
	return r.created, r.replayed, r.createErr
}

func (r *stubRepository) Get(_ context.Context, workspaceID, jobID string) (Job, error) {
	r.getWorkspaceID = workspaceID
	r.getJobID = jobID
	return r.got, r.getErr
}
