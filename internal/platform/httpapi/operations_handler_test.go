package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/operations"
)

func TestAdminJobsPassesFiltersPaginationAndRecordsAudit(t *testing.T) {
	next := "next-job"
	service := &fakeOperationsService{jobs: operations.JobList{
		Items: []operations.JobSummary{{
			ID: "30000000-0000-4000-8000-000000000081", Kind: "mock_transcribe", State: "failed",
		}},
		NextCursor: &next,
	}}
	auditService := &fakeAuditService{}
	handler := operationsHTTPHandler(service, auditService)
	request := authenticatedOperationsRequest(http.MethodGet,
		"/api/v1/admin/jobs?limit=25&cursor=previous&state=failed&kind=mock_transcribe")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	wantInput := operations.JobListInput{Limit: 25, Cursor: "previous", State: "failed", Kind: "mock_transcribe"}
	if service.jobInput != wantInput {
		t.Fatalf("job input = %+v, want %+v", service.jobInput, wantInput)
	}
	var response operations.JobList
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Items) != 1 || response.NextCursor == nil {
		t.Fatalf("decode jobs = (%+v, %v)", response, err)
	}
	if auditService.input.Action != "admin.job.listed" || auditService.input.TargetType != "job_collection" ||
		auditService.input.Metadata["state_filter"] != true {
		t.Fatalf("audit input = %+v", auditService.input)
	}
}

func TestAdminAuditLogsPassesFiltersAndFailsClosedOnAuditWrite(t *testing.T) {
	service := &fakeOperationsService{audits: operations.AuditList{Items: []operations.AuditEntry{{
		ID: "40000000-0000-4000-8000-000000000081", ActorType: "agent",
		Action: "asset.read", TargetType: "asset", Metadata: json.RawMessage(`{}`),
	}}}}
	auditService := &fakeAuditService{err: errors.New("database unavailable")}
	handler := operationsHTTPHandler(service, auditService)
	request := authenticatedOperationsRequest(http.MethodGet,
		"/api/v1/admin/audit-logs?limit=10&actor_type=agent&action=asset.read&target_type=asset")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	want := operations.AuditListInput{Limit: 10, ActorType: "agent", Action: "asset.read", TargetType: "asset"}
	if service.auditInput != want {
		t.Fatalf("audit list input = %+v, want %+v", service.auditInput, want)
	}
}

func TestAdminSystemStatusReturnsSnapshot(t *testing.T) {
	generatedAt := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	service := &fakeOperationsService{status: operations.SystemStatus{
		GeneratedAt: generatedAt, ActiveUsers: 2,
		Assets: operations.AssetStatus{Total: 4, Active: 3},
	}}
	auditService := &fakeAuditService{}
	handler := operationsHTTPHandler(service, auditService)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, authenticatedOperationsRequest(http.MethodGet, "/api/v1/admin/system-status"))

	if recorder.Code != http.StatusOK || service.statusCalls != 1 || auditService.input.Action != "admin.system_status.read" {
		t.Fatalf("response/calls/audit = %d %d %+v: %s", recorder.Code, service.statusCalls, auditService.input, recorder.Body.String())
	}
}

func TestAdminJobRetryUsesWriteAuthenticationAndReturnsSafeJob(t *testing.T) {
	jobID := "30000000-0000-4000-8000-000000000082"
	service := &fakeOperationsService{retryResult: operations.JobSummary{
		ID: jobID, Kind: "mock_transcribe", State: "queued", Attempts: 3, MaxAttempts: 4,
	}}
	handler := operationsHTTPHandler(service, &fakeAuditService{})
	request := authenticatedOperationsRequest(http.MethodPost, "/api/v1/admin/jobs/"+jobID+"/retry")
	request.Header.Set("X-Request-ID", "mobile-retry")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || service.retryJobID != jobID ||
		service.retryRequestID != "mobile-retry" || !strings.Contains(recorder.Body.String(), `"state":"queued"`) {
		t.Fatalf("retry response/call = %d %q/%q: %s", recorder.Code, service.retryJobID, service.retryRequestID, recorder.Body.String())
	}
}

func TestAdminJobRetryMapsDomainErrorsAndRejectsWrongRoutes(t *testing.T) {
	jobID := "30000000-0000-4000-8000-000000000082"
	service := &fakeOperationsService{}
	handler := operationsHTTPHandler(service, &fakeAuditService{})
	tests := []struct {
		name       string
		err        error
		statusCode int
		code       string
	}{
		{name: "invalid", err: operations.ErrInvalidInput, statusCode: http.StatusBadRequest, code: "invalid_request"},
		{name: "forbidden", err: operations.ErrForbidden, statusCode: http.StatusForbidden, code: "forbidden"},
		{name: "missing", err: operations.ErrNotFound, statusCode: http.StatusNotFound, code: "not_found"},
		{name: "not retryable", err: operations.ErrNotRetryable, statusCode: http.StatusConflict, code: "job_not_retryable"},
		{name: "limit", err: operations.ErrRetryLimit, statusCode: http.StatusConflict, code: "job_retry_limit_reached"},
		{name: "internal", err: errors.New("database unavailable"), statusCode: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service.retryErr = test.err
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, authenticatedOperationsRequest(http.MethodPost, "/api/v1/admin/jobs/"+jobID+"/retry"))
			if recorder.Code != test.statusCode || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}

	service.retryErr = nil
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedOperationsRequest(http.MethodGet, "/api/v1/admin/jobs/"+jobID+"/retry"))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d: %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedOperationsRequest(http.MethodPost, "/api/v1/admin/jobs/not-a-job/retry"))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("route status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminOperationsRejectsInvalidQueryMethodAndScope(t *testing.T) {
	service := &fakeOperationsService{}
	handler := operationsHTTPHandler(service, &fakeAuditService{})
	for name, target := range map[string]string{
		"duplicate": "/api/v1/admin/jobs?limit=1&limit=2",
		"unknown":   "/api/v1/admin/jobs?raw_path=secret",
		"limit":     "/api/v1/admin/audit-logs?limit=nope",
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, authenticatedOperationsRequest(http.MethodGet, target))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedOperationsRequest(http.MethodPost, "/api/v1/admin/jobs"))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d", recorder.Code)
	}

	service.jobErr = operations.ErrForbidden
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedOperationsRequest(http.MethodGet, "/api/v1/admin/jobs"))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "admin:read") {
		t.Fatalf("scope response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func operationsHTTPHandler(service OperationsService, auditService AuditService) http.Handler {
	return NewApplicationHandler(Options{
		AuthService: &fakeAuthService{principal: auth.Principal{
			UserID: "20000000-0000-4000-8000-000000000081", WorkspaceID: "10000000-0000-4000-8000-000000000081",
			Scopes: []string{auth.ScopeAdminRead},
		}},
		OperationsService: service,
		AuditService:      auditService,
	})
}

func authenticatedOperationsRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	return request
}

type fakeOperationsService struct {
	jobs           operations.JobList
	jobInput       operations.JobListInput
	jobErr         error
	audits         operations.AuditList
	auditInput     operations.AuditListInput
	auditErr       error
	status         operations.SystemStatus
	statusErr      error
	statusCalls    int
	retryResult    operations.JobSummary
	retryJobID     string
	retryRequestID string
	retryErr       error
}

func (service *fakeOperationsService) ListJobs(
	_ context.Context,
	_ auth.Principal,
	input operations.JobListInput,
) (operations.JobList, error) {
	service.jobInput = input
	return service.jobs, service.jobErr
}

func (service *fakeOperationsService) ListAuditLogs(
	_ context.Context,
	_ auth.Principal,
	input operations.AuditListInput,
) (operations.AuditList, error) {
	service.auditInput = input
	return service.audits, service.auditErr
}

func (service *fakeOperationsService) GetSystemStatus(
	_ context.Context,
	_ auth.Principal,
) (operations.SystemStatus, error) {
	service.statusCalls++
	return service.status, service.statusErr
}

func (service *fakeOperationsService) RetryJob(
	_ context.Context,
	_ auth.Principal,
	jobID string,
	requestID string,
) (operations.JobSummary, error) {
	service.retryJobID = jobID
	service.retryRequestID = requestID
	return service.retryResult, service.retryErr
}

var _ OperationsService = (*fakeOperationsService)(nil)
