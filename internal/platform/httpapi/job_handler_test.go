package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/job"
)

func TestStartTranscriptionReturnsAcceptedJob(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1",
		Scopes: []string{auth.ScopeTranscriptionsWrite},
	}}
	jobService := &fakeJobService{created: job.Job{
		ID: "job-1", WorkspaceID: "workspace-1", AssetID: "asset-1",
		Kind: job.KindMockTranscribe, State: job.StateQueued,
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, JobService: jobService,
		PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/assets/"+httpAssetID+"/transcriptions", nil)
	request.Header.Set("Idempotency-Key", "transcribe-asset-1")
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusAccepted, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/v1/transcription-jobs/job-1" {
		t.Fatalf("Location = %q", recorder.Header().Get("Location"))
	}
	if jobService.assetID != httpAssetID || jobService.idempotencyKey != "transcribe-asset-1" {
		t.Fatalf("service args = %q/%q", jobService.assetID, jobService.idempotencyKey)
	}
	var response job.Job
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.ID != "job-1" {
		t.Fatalf("decode job = (%+v, %v)", response, err)
	}
}

func TestStartTranscriptionMarksIdempotentReplay(t *testing.T) {
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptionsWrite},
		}},
		JobService:   &fakeJobService{created: job.Job{ID: "job-1"}, replayed: true},
		PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/assets/"+httpAssetID+"/transcriptions", nil)
	request.Header.Set("Idempotency-Key", "transcribe-asset-1")
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay response = %d/%v", recorder.Code, recorder.Header())
	}
}

func TestGetTranscriptionJobIsWorkspaceScoped(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptsRead},
	}}
	jobService := &fakeJobService{got: job.Job{ID: "job-1", State: job.StateSucceeded}}
	auditService := &fakeAuditService{}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, JobService: jobService, AuditService: auditService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/transcription-jobs/"+httpJobID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || jobService.jobID != httpJobID {
		t.Fatalf("GET job response = %d, ID = %q", recorder.Code, jobService.jobID)
	}
	if auditService.input.Action != "job.read" || auditService.input.TargetID != "job-1" {
		t.Fatalf("audit input = %+v", auditService.input)
	}
}

func TestTranscriptionJobErrorsAreSafe(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "not found", err: job.ErrNotFound, status: http.StatusNotFound},
		{name: "not ready", err: job.ErrAssetNotReady, status: http.StatusConflict},
		{name: "idempotency", err: job.ErrIdempotencyConflict, status: http.StatusConflict},
		{name: "invalid", err: job.ErrInvalidInput, status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewApplicationHandler(Options{
				BrandName: "VoiceAsset",
				AuthService: &fakeAuthService{principal: auth.Principal{
					WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptionsWrite},
				}},
				JobService:   &fakeJobService{createErr: test.err},
				PublicOrigin: "https://voice.example.com",
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/assets/"+httpAssetID+"/transcriptions", nil)
			request.Header.Set("Idempotency-Key", "key")
			request.Header.Set("Origin", "https://voice.example.com")
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
		})
	}
}

type fakeJobService struct {
	created        job.Job
	replayed       bool
	createErr      error
	assetID        string
	idempotencyKey string
	got            job.Job
	getErr         error
	jobID          string
}

func (f *fakeJobService) CreateTranscription(
	_ context.Context,
	_ auth.Principal,
	assetID,
	idempotencyKey string,
) (job.Job, bool, error) {
	f.assetID = assetID
	f.idempotencyKey = idempotencyKey
	return f.created, f.replayed, f.createErr
}

func (f *fakeJobService) Get(_ context.Context, _ auth.Principal, jobID string) (job.Job, error) {
	f.jobID = jobID
	return f.got, f.getErr
}

var _ JobService = (*fakeJobService)(nil)
