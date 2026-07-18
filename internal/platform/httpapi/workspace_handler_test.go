package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/workspace"
)

const handlerWorkspaceID = "10000000-0000-4000-8000-000000000001"

func TestAdminWorkspaceGetReturnsVersionAndFailsClosedOnReadAudit(t *testing.T) {
	service := &fakeWorkspaceService{got: workspace.Workspace{ID: handlerWorkspaceID, Name: "Primary", Version: 3}}
	auditService := &fakeAuditService{}
	handler := workspaceHTTPHandler(service, auditService)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, authenticatedWorkspaceRequest(http.MethodGet, "/api/v1/admin/workspace", ""))

	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"3"` ||
		auditService.input.Action != "workspace.read" || auditService.input.TargetID != handlerWorkspaceID ||
		auditService.input.Metadata["version"] != int64(3) {
		t.Fatalf("response/audit = %d headers=%v body=%s audit=%+v", recorder.Code, recorder.Header(), recorder.Body.String(), auditService.input)
	}

	auditService.err = errors.New("database unavailable")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedWorkspaceRequest(http.MethodGet, "/api/v1/admin/workspace", ""))
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("audit failure = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminWorkspacePatchRequiresExactVersion(t *testing.T) {
	service := &fakeWorkspaceService{updated: workspace.Workspace{ID: handlerWorkspaceID, Name: "Renamed", Version: 4}}
	handler := workspaceHTTPHandler(service, &fakeAuditService{})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, authenticatedWorkspaceRequest(http.MethodPatch, "/api/v1/admin/workspace", `{"name":"Renamed"}`))
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match = %d %s", recorder.Code, recorder.Body.String())
	}

	request := authenticatedWorkspaceRequest(http.MethodPatch, "/api/v1/admin/workspace", `{"name":"Renamed"}`)
	request.Header.Set("If-Match", `"3"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"4"` ||
		service.expectedVersion != 3 || service.updateInput.Name != "Renamed" {
		t.Fatalf("patch = %d headers=%v body=%s service=%+v", recorder.Code, recorder.Header(), recorder.Body.String(), service)
	}

	service.updateErr = workspace.ErrVersionConflict
	request = authenticatedWorkspaceRequest(http.MethodPatch, "/api/v1/admin/workspace", `{"name":"Again"}`)
	request.Header.Set("If-Match", `"3"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPreconditionFailed || !strings.Contains(recorder.Body.String(), `"code":"version_conflict"`) {
		t.Fatalf("version conflict = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminWorkspaceRejectsQueryMethodsAndOversizedBody(t *testing.T) {
	handler := workspaceHTTPHandler(&fakeWorkspaceService{}, &fakeAuditService{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedWorkspaceRequest(http.MethodGet, "/api/v1/admin/workspace?include=members", ""))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("query = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedWorkspaceRequest(http.MethodDelete, "/api/v1/admin/workspace", ""))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method = %d %s", recorder.Code, recorder.Body.String())
	}
	request := authenticatedWorkspaceRequest(
		http.MethodPatch, "/api/v1/admin/workspace", `{"name":"`+strings.Repeat("x", maxWorkspaceBodyBytes)+`"}`,
	)
	request.Header.Set("If-Match", `"1"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), `"code":"request_too_large"`) {
		t.Fatalf("oversized body = %d %s", recorder.Code, recorder.Body.String())
	}
}

func workspaceHTTPHandler(service WorkspaceService, auditService AuditService) http.Handler {
	return NewApplicationHandler(Options{
		AuthService: &fakeAuthService{principal: auth.Principal{
			UserID: "20000000-0000-4000-8000-000000000001", WorkspaceID: handlerWorkspaceID,
			Role: "owner", Scopes: auth.AllScopes(),
		}},
		WorkspaceService: service,
		AuditService:     auditService,
	})
}

func authenticatedWorkspaceRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

type fakeWorkspaceService struct {
	got             workspace.Workspace
	getErr          error
	updated         workspace.Workspace
	expectedVersion int64
	updateInput     workspace.UpdateInput
	updateErr       error
}

func (service *fakeWorkspaceService) Get(_ context.Context, _ auth.Principal) (workspace.Workspace, error) {
	return service.got, service.getErr
}

func (service *fakeWorkspaceService) Update(
	_ context.Context,
	_ auth.Principal,
	expectedVersion int64,
	input workspace.UpdateInput,
	_ string,
) (workspace.Workspace, error) {
	service.expectedVersion = expectedVersion
	service.updateInput = input
	return service.updated, service.updateErr
}

var _ WorkspaceService = (*fakeWorkspaceService)(nil)
