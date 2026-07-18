package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/membership"
)

func TestAdminMembersListsFiltersAndFailsClosedOnReadAudit(t *testing.T) {
	next := "next-members"
	service := &fakeMembershipService{listed: membership.List{
		Items:      []membership.Member{{ID: "30000000-0000-4000-8000-000000000001", Email: "member@example.test"}},
		NextCursor: &next,
	}}
	auditService := &fakeAuditService{}
	handler := membershipHTTPHandler(service, auditService)
	request := authenticatedMembershipRequest(http.MethodGet, "/api/v1/admin/members?limit=25&cursor=previous&role=editor&status=active", "")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	want := membership.ListInput{Limit: 25, Cursor: "previous", Role: "editor", Status: "active"}
	if service.listInput != want || auditService.input.Action != "membership.listed" ||
		auditService.input.Metadata["result_count"] != 1 {
		t.Fatalf("input/audit = %+v %+v", service.listInput, auditService.input)
	}

	auditService.err = errors.New("database unavailable")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedMembershipRequest(http.MethodGet, "/api/v1/admin/members", ""))
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("audit failure = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminMemberCreateKeepsPasswordOutOfResponse(t *testing.T) {
	service := &fakeMembershipService{created: membership.Member{
		ID: "30000000-0000-4000-8000-000000000001", WorkspaceID: "10000000-0000-4000-8000-000000000001",
		Email: "member@example.test", Role: "viewer", Status: "active", Version: 1,
	}}
	handler := membershipHTTPHandler(service, &fakeAuditService{})
	body := `{"email":"member@example.test","password":"long-test-password","role":"viewer"}`
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, authenticatedMembershipRequest(http.MethodPost, "/api/v1/admin/members", body))

	if recorder.Code != http.StatusCreated || recorder.Header().Get("ETag") != `"1"` ||
		recorder.Header().Get("Location") != "/api/v1/admin/members/"+service.created.ID {
		t.Fatalf("response = %d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if service.createInput.Password != "long-test-password" || strings.Contains(recorder.Body.String(), "password") {
		t.Fatalf("create input/response = %+v %s", service.createInput, recorder.Body.String())
	}
}

func TestAdminMemberPatchRequiresVersionAndMapsOwnerSafety(t *testing.T) {
	memberID := "30000000-0000-4000-8000-000000000001"
	service := &fakeMembershipService{updated: membership.Member{ID: memberID, Role: "admin", Status: "active", Version: 2}}
	handler := membershipHTTPHandler(service, &fakeAuditService{})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, authenticatedMembershipRequest(http.MethodPatch, "/api/v1/admin/members/"+memberID, `{"role":"admin"}`))
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match = %d %s", recorder.Code, recorder.Body.String())
	}

	request := authenticatedMembershipRequest(http.MethodPatch, "/api/v1/admin/members/"+memberID, `{"role":"admin"}`)
	request.Header.Set("If-Match", `"1"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"2"` || service.expectedVersion != 1 {
		t.Fatalf("patch = %d headers=%v version=%d body=%s", recorder.Code, recorder.Header(), service.expectedVersion, recorder.Body.String())
	}

	service.updateErr = membership.ErrLastOwner
	request = authenticatedMembershipRequest(http.MethodPatch, "/api/v1/admin/members/"+memberID, `{"status":"disabled"}`)
	request.Header.Set("If-Match", `"2"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"last_owner_required"`) {
		t.Fatalf("last owner = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminMembersRejectsUnknownQueryAndUnsupportedMethods(t *testing.T) {
	handler := membershipHTTPHandler(&fakeMembershipService{}, &fakeAuditService{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedMembershipRequest(http.MethodGet, "/api/v1/admin/members?email=secret@example.test", ""))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown query = %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedMembershipRequest(http.MethodDelete, "/api/v1/admin/members", ""))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestAdminMemberMutationsRejectOversizedBodies(t *testing.T) {
	service := &fakeMembershipService{}
	handler := membershipHTTPHandler(service, &fakeAuditService{})
	oversized := strings.Repeat("x", maxMembershipBodyBytes)

	create := authenticatedMembershipRequest(
		http.MethodPost,
		"/api/v1/admin/members",
		`{"email":"member@example.test","password":"`+oversized+`","role":"viewer"}`,
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, create)
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), `"code":"request_too_large"`) {
		t.Fatalf("oversized create = %d %s", recorder.Code, recorder.Body.String())
	}

	memberID := "30000000-0000-4000-8000-000000000001"
	update := authenticatedMembershipRequest(
		http.MethodPatch,
		"/api/v1/admin/members/"+memberID,
		`{"role":"`+oversized+`"}`,
	)
	update.Header.Set("If-Match", `"1"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, update)
	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), `"code":"request_too_large"`) {
		t.Fatalf("oversized update = %d %s", recorder.Code, recorder.Body.String())
	}
}

func membershipHTTPHandler(service MembershipService, auditService AuditService) http.Handler {
	return NewApplicationHandler(Options{
		AuthService: &fakeAuthService{principal: auth.Principal{
			UserID: "20000000-0000-4000-8000-000000000001", WorkspaceID: "10000000-0000-4000-8000-000000000001",
			Role: "owner", Scopes: auth.AllScopes(),
		}},
		MembershipService: service,
		AuditService:      auditService,
	})
}

func authenticatedMembershipRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

type fakeMembershipService struct {
	created         membership.Member
	createInput     membership.CreateInput
	createErr       error
	listed          membership.List
	listInput       membership.ListInput
	listErr         error
	updated         membership.Member
	updateInput     membership.UpdateInput
	expectedVersion int64
	updateErr       error
}

func (service *fakeMembershipService) Create(
	_ context.Context,
	_ auth.Principal,
	input membership.CreateInput,
	_ string,
) (membership.Member, error) {
	service.createInput = input
	return service.created, service.createErr
}

func (service *fakeMembershipService) List(
	_ context.Context,
	_ auth.Principal,
	input membership.ListInput,
) (membership.List, error) {
	service.listInput = input
	return service.listed, service.listErr
}

func (service *fakeMembershipService) Update(
	_ context.Context,
	_ auth.Principal,
	_ string,
	expectedVersion int64,
	input membership.UpdateInput,
	_ string,
) (membership.Member, error) {
	service.expectedVersion = expectedVersion
	service.updateInput = input
	return service.updated, service.updateErr
}

var _ MembershipService = (*fakeMembershipService)(nil)

func decodeMembershipResponse(t *testing.T, recorder *httptest.ResponseRecorder) membership.Member {
	t.Helper()
	var member membership.Member
	if err := json.Unmarshal(recorder.Body.Bytes(), &member); err != nil {
		t.Fatalf("decode member response: %v", err)
	}
	return member
}
