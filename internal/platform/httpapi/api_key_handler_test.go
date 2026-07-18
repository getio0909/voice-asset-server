package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/apikey"
	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestCreateAPIKeyReturnsOneTimeToken(t *testing.T) {
	expiresAt := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	authService := &fakeAuthService{principal: auth.Principal{
		UserID:      "20000000-0000-4000-8000-000000000001",
		WorkspaceID: "10000000-0000-4000-8000-000000000001",
		Scopes:      []string{auth.ScopeAdminWrite, auth.ScopeAssetsRead},
	}}
	apiKeyService := &fakeAPIKeyService{created: apikey.CreateResult{
		APIKey: apikey.APIKey{
			ID: "30000000-0000-4000-8000-000000000001", Name: "MCP reader",
			TokenPrefix: "va_pat_12345678", Scopes: []string{auth.ScopeAssetsRead}, ExpiresAt: expiresAt,
		},
		Token: "va_pat_one_time_secret",
	}}
	handler := NewApplicationHandler(Options{AuthService: authService, APIKeyService: apiKeyService})
	body := `{"name":"MCP reader","scopes":["assets:read"],"expires_at":"2026-08-16T12:00:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "api-key-create-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/v1/api-keys/30000000-0000-4000-8000-000000000001" {
		t.Fatalf("Location = %q", recorder.Header().Get("Location"))
	}
	if apiKeyService.createInput.Name != "MCP reader" || apiKeyService.requestID != "api-key-create-1" {
		t.Fatalf("Create args = %+v/%q", apiKeyService.createInput, apiKeyService.requestID)
	}
	var response apikey.CreateResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Token != apiKeyService.created.Token {
		t.Fatalf("decode response = (%+v, %v)", response, err)
	}
}

func TestListAPIKeysIsRedactedAndAudited(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID:      "20000000-0000-4000-8000-000000000001",
		WorkspaceID: "10000000-0000-4000-8000-000000000001", Scopes: []string{auth.ScopeAdminRead},
	}}
	apiKeyService := &fakeAPIKeyService{listed: []apikey.APIKey{{
		ID: "30000000-0000-4000-8000-000000000001", TokenPrefix: "va_pat_12345678",
	}}}
	auditService := &fakeAuditService{}
	handler := NewApplicationHandler(Options{
		AuthService: authService, APIKeyService: apiKeyService, AuditService: auditService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/api-keys", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "one_time_secret") {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if auditService.calls != 1 || auditService.input.Action != "api_key.listed" {
		t.Fatalf("audit = %+v/%d", auditService.input, auditService.calls)
	}
}

func TestRevokeAPIKeyUsesWorkspaceAuthenticatedService(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		WorkspaceID: "10000000-0000-4000-8000-000000000001", Scopes: []string{auth.ScopeAdminWrite},
	}}
	revokedAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	apiKeyService := &fakeAPIKeyService{revoked: apikey.APIKey{
		ID: "30000000-0000-4000-8000-000000000001", RevokedAt: &revokedAt,
	}}
	handler := NewApplicationHandler(Options{AuthService: authService, APIKeyService: apiKeyService})
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/api-keys/30000000-0000-4000-8000-000000000001", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || apiKeyService.revokeID != "30000000-0000-4000-8000-000000000001" {
		t.Fatalf("response/id = %d %s / %q", recorder.Code, recorder.Body.String(), apiKeyService.revokeID)
	}
}

type fakeAPIKeyService struct {
	created     apikey.CreateResult
	createErr   error
	createInput apikey.CreateInput
	requestID   string
	listed      []apikey.APIKey
	listErr     error
	revoked     apikey.APIKey
	revokeErr   error
	revokeID    string
}

func (service *fakeAPIKeyService) Create(
	_ context.Context,
	_ auth.Principal,
	input apikey.CreateInput,
	requestID string,
) (apikey.CreateResult, error) {
	service.createInput = input
	service.requestID = requestID
	return service.created, service.createErr
}

func (service *fakeAPIKeyService) List(context.Context, auth.Principal) ([]apikey.APIKey, error) {
	return service.listed, service.listErr
}

func (service *fakeAPIKeyService) Revoke(
	_ context.Context,
	_ auth.Principal,
	keyID string,
	requestID string,
) (apikey.APIKey, error) {
	service.revokeID = keyID
	service.requestID = requestID
	return service.revoked, service.revokeErr
}

var _ APIKeyService = (*fakeAPIKeyService)(nil)
