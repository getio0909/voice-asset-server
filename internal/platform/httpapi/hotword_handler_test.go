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
	"github.com/getio0909/voice-asset-server/internal/hotword"
)

const httpHotwordSetID = "90000000-0000-4000-8000-000000000011"

func TestHotwordSetCollectionRoutesCreateAndList(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1",
		Scopes: []string{auth.ScopeAdminRead, auth.ScopeAdminWrite},
	}}
	service := &fakeHotwordService{
		created: hotword.Set{
			ID: httpHotwordSetID, WorkspaceID: "workspace-1", DisplayName: "Product terms",
			ScopeType: hotword.ScopeWorkspace, State: hotword.StateEnabled,
			CurrentVersion: 1, ResourceVersion: 1,
		},
		listed: []hotword.Set{{ID: httpHotwordSetID, ResourceVersion: 1}},
	}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, HotwordService: service,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/hotword-sets", strings.NewReader(`{
		"display_name":"Product terms","scope_type":"workspace",
		"entries":[{"term":"VoiceAsset","language":"zh-CN","weight":90}]
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated ||
		recorder.Header().Get("Location") != "/api/v1/hotword-sets/"+httpHotwordSetID ||
		recorder.Header().Get("ETag") != `"1"` || service.createPrincipal.WorkspaceID != "workspace-1" ||
		len(service.createInput.Entries) != 1 || service.createInput.Entries[0].Term != "VoiceAsset" {
		t.Fatalf("create status = %d, headers = %v, service = %+v, body = %s", recorder.Code, recorder.Header(), service, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/hotword-sets", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var response struct {
		Items []hotword.Set `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil ||
		recorder.Code != http.StatusOK || len(response.Items) != 1 ||
		service.listPrincipal.WorkspaceID != "workspace-1" {
		t.Fatalf("list status = %d, response = %+v, error = %v", recorder.Code, response, err)
	}
}

func TestHotwordSetMutationsRequireETagAndReturnNewVersion(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAdminWrite},
	}}
	service := &fakeHotwordService{
		versioned: hotword.Set{ID: httpHotwordSetID, CurrentVersion: 2, ResourceVersion: 2},
		updated:   hotword.Set{ID: httpHotwordSetID, State: hotword.StateDisabled, ResourceVersion: 3},
	}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, HotwordService: service,
	})
	request := httptest.NewRequest(
		http.MethodPost, "/api/v1/hotword-sets/"+httpHotwordSetID+"/versions",
		strings.NewReader(`{"entries":[{"term":"VoiceAsset","language":"zh-CN","weight":95}]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("If-Match", `"1"`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || recorder.Header().Get("ETag") != `"2"` ||
		service.versionSetID != httpHotwordSetID || service.versionExpected != 1 ||
		len(service.versionInput.Entries) != 1 {
		t.Fatalf("version status = %d, headers = %v, service = %+v, body = %s", recorder.Code, recorder.Header(), service, recorder.Body.String())
	}
	disabledBody := `{"state":"disabled"}`
	request = httptest.NewRequest(http.MethodPatch, "/api/v1/hotword-sets/"+httpHotwordSetID, strings.NewReader(disabledBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("If-Match", `"2"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"3"` ||
		service.updateSetID != httpHotwordSetID || service.updateExpected != 2 ||
		service.updateInput.State == nil || *service.updateInput.State != hotword.StateDisabled {
		t.Fatalf("update status = %d, headers = %v, service = %+v, body = %s", recorder.Code, recorder.Header(), service, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPatch, "/api/v1/hotword-sets/"+httpHotwordSetID, strings.NewReader(disabledBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestHotwordHandlerMapsSafeErrors(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{Scopes: []string{auth.ScopeAdminWrite}}}
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid", err: hotword.ErrInvalidInput, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "forbidden", err: hotword.ErrForbidden, status: http.StatusForbidden, code: "forbidden"},
		{name: "conflict", err: hotword.ErrConflict, status: http.StatusConflict, code: "hotword_conflict"},
		{name: "not found", err: hotword.ErrNotFound, status: http.StatusNotFound, code: "not_found"},
		{name: "repository", err: errors.New("database secret detail"), status: http.StatusInternalServerError, code: "internal_error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeHotwordService{createErr: test.err}
			handler := NewApplicationHandler(Options{
				BrandName: "VoiceAsset", AuthService: authService, HotwordService: service,
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/hotword-sets", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) ||
				strings.Contains(recorder.Body.String(), "secret") {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

type fakeHotwordService struct {
	createPrincipal auth.Principal
	createInput     hotword.CreateInput
	created         hotword.Set
	createErr       error
	listPrincipal   auth.Principal
	listed          []hotword.Set
	listErr         error
	versionSetID    string
	versionExpected int64
	versionInput    hotword.AddVersionInput
	versioned       hotword.Set
	versionErr      error
	updateSetID     string
	updateExpected  int64
	updateInput     hotword.UpdateInput
	updated         hotword.Set
	updateErr       error
}

func (service *fakeHotwordService) Create(
	_ context.Context,
	principal auth.Principal,
	input hotword.CreateInput,
) (hotword.Set, error) {
	service.createPrincipal = principal
	service.createInput = input
	return service.created, service.createErr
}

func (service *fakeHotwordService) List(
	_ context.Context,
	principal auth.Principal,
) ([]hotword.Set, error) {
	service.listPrincipal = principal
	return service.listed, service.listErr
}

func (service *fakeHotwordService) AddVersion(
	_ context.Context,
	_ auth.Principal,
	setID string,
	expectedResourceVersion int64,
	input hotword.AddVersionInput,
) (hotword.Set, error) {
	service.versionSetID = setID
	service.versionExpected = expectedResourceVersion
	service.versionInput = input
	return service.versioned, service.versionErr
}

func (service *fakeHotwordService) Update(
	_ context.Context,
	_ auth.Principal,
	setID string,
	expectedResourceVersion int64,
	input hotword.UpdateInput,
) (hotword.Set, error) {
	service.updateSetID = setID
	service.updateExpected = expectedResourceVersion
	service.updateInput = input
	return service.updated, service.updateErr
}

var _ HotwordService = (*fakeHotwordService)(nil)
