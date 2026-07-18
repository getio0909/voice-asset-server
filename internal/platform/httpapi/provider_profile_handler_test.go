package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/providerprofile"
)

func TestCreateProviderProfileRequiresAdminAndNeverEchoesCredentials(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAdminWrite},
	}}
	created := providerprofile.Profile{
		ID: "90000000-0000-4000-8000-000000000001", WorkspaceID: "workspace-1",
		ProviderID: asr.TencentProviderID, DisplayName: "Tencent", State: providerprofile.StateEnabled,
		Priority: 10, Version: 1, SecretConfigured: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	service := &fakeProviderProfileService{created: created}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, ProviderService: service,
		PublicOrigin: "https://voice.example.com",
	})
	const credentialValue = "fixture-secret-key"
	body := `{
		"provider_id":"tencent_asr",
		"display_name":"Tencent",
		"config":{
			"model":"16k_zh","language":"zh-CN","sample_rate":16000,"audio_format":"m4a",
			"punctuation":true,"timestamps":true,"word_timestamps":true,
			"speaker_diarization":false,"number_normalization":true,
			"timeout":"2m","retry":{"max_attempts":3,"base_delay":"1s","max_delay":"30s"},
			"concurrency":20,"vendor_extension":{"appid":"1234567890"}
		},
		"credentials":{"secret_id":"fixture-secret-id","secret_key":"` + credentialValue + `"},
		"state":"enabled","priority":10
	}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/provider-profiles", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/v1/provider-profiles/"+created.ID ||
		recorder.Header().Get("ETag") != `"1"` {
		t.Fatalf("resource headers = %v", recorder.Header())
	}
	if !strings.Contains(string(service.input.Credentials), credentialValue) {
		t.Fatal("handler did not pass credential JSON to the service boundary")
	}
	if strings.Contains(recorder.Body.String(), credentialValue) || strings.Contains(recorder.Body.String(), "credentials") {
		t.Fatalf("response echoed credential material: %s", recorder.Body.String())
	}
}

func TestListProviderProfilesUsesAuthenticatedWorkspaceService(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAdminRead},
	}}
	service := &fakeProviderProfileService{listed: []providerprofile.Profile{{
		ID: "profile-1", WorkspaceID: "workspace-1", ProviderID: asr.MockProviderID,
	}}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, ProviderService: service,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/provider-profiles", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.listPrincipal.WorkspaceID != "workspace-1" {
		t.Fatalf("status = %d, principal = %+v, body = %s", recorder.Code, service.listPrincipal, recorder.Body.String())
	}
	var response struct {
		Items []providerprofile.Profile `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Items) != 1 {
		t.Fatalf("response = %+v, error = %v", response, err)
	}
}

func TestProviderProfileHandlerMapsSafeErrors(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{Scopes: []string{auth.ScopeAdminWrite}}}
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid", err: providerprofile.ErrInvalidInput, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "forbidden", err: providerprofile.ErrForbidden, status: http.StatusForbidden, code: "forbidden"},
		{name: "conflict", err: providerprofile.ErrConflict, status: http.StatusConflict, code: "profile_conflict"},
		{name: "no encryption", err: providerprofile.ErrEncryptionUnavailable, status: http.StatusServiceUnavailable, code: "encryption_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeProviderProfileService{createErr: test.err}
			handler := NewApplicationHandler(Options{
				BrandName: "VoiceAsset", AuthService: authService, ProviderService: service,
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/provider-profiles", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status || !strings.Contains(recorder.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestUpdateProviderProfileRequiresVersionAndReturnsNewETag(t *testing.T) {
	const profileID = "90000000-0000-4000-8000-000000000001"
	authService := &fakeAuthService{principal: auth.Principal{Scopes: []string{auth.ScopeAdminWrite}}}
	service := &fakeProviderProfileService{updated: providerprofile.Profile{
		ID: profileID, Version: 5, State: providerprofile.StateDisabled,
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, ProviderService: service,
	})
	request := httptest.NewRequest(
		http.MethodPatch, "/api/v1/provider-profiles/"+profileID,
		strings.NewReader(`{"state":"disabled"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("If-Match", `"4"`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"5"` ||
		service.updateID != profileID || service.updateVersion != 4 ||
		service.updateInput.State == nil || *service.updateInput.State != providerprofile.StateDisabled {
		t.Fatalf("status = %d, headers = %v, service = %+v, body = %s", recorder.Code, recorder.Header(), service, recorder.Body.String())
	}

	request = httptest.NewRequest(
		http.MethodPatch, "/api/v1/provider-profiles/"+profileID,
		strings.NewReader(`{"state":"enabled"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestProviderHealthAndCapabilitiesRoutes(t *testing.T) {
	const profileID = "90000000-0000-4000-8000-000000000001"
	authService := &fakeAuthService{principal: auth.Principal{Scopes: []string{auth.ScopeAdminRead}}}
	service := &fakeProviderProfileService{
		health:       providerprofile.Health{ProfileID: profileID, Status: "healthy", CheckedAt: time.Now().UTC()},
		capabilities: asr.BuiltInCapabilities(),
	}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, ProviderService: service,
	})
	healthRequest := httptest.NewRequest(http.MethodPost, "/api/v1/provider-profiles/"+profileID+"/health", nil)
	healthRequest.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	healthRecorder := httptest.NewRecorder()
	handler.ServeHTTP(healthRecorder, healthRequest)
	if healthRecorder.Code != http.StatusOK || service.healthID != profileID ||
		!strings.Contains(healthRecorder.Body.String(), `"status":"healthy"`) {
		t.Fatalf("health status = %d, body = %s", healthRecorder.Code, healthRecorder.Body.String())
	}

	capabilitiesRequest := httptest.NewRequest(http.MethodGet, "/api/v1/asr/provider-capabilities", nil)
	capabilitiesRequest.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	capabilitiesRecorder := httptest.NewRecorder()
	handler.ServeHTTP(capabilitiesRecorder, capabilitiesRequest)
	if capabilitiesRecorder.Code != http.StatusOK ||
		!strings.Contains(capabilitiesRecorder.Body.String(), `"provider_id":"aliyun_asr"`) ||
		!strings.Contains(capabilitiesRecorder.Body.String(), `"provider_id":"tencent_asr"`) {
		t.Fatalf("capabilities status = %d, body = %s", capabilitiesRecorder.Code, capabilitiesRecorder.Body.String())
	}
}

type fakeProviderProfileService struct {
	input           providerprofile.CreateInput
	createPrincipal auth.Principal
	created         providerprofile.Profile
	createErr       error
	listPrincipal   auth.Principal
	listed          []providerprofile.Profile
	listErr         error
	updated         providerprofile.Profile
	updateErr       error
	updateID        string
	updateVersion   int64
	updateInput     providerprofile.UpdateInput
	health          providerprofile.Health
	healthErr       error
	healthID        string
	capabilities    []asr.Capabilities
	capabilitiesErr error
}

func (service *fakeProviderProfileService) Create(
	_ context.Context,
	principal auth.Principal,
	input providerprofile.CreateInput,
) (providerprofile.Profile, error) {
	service.createPrincipal = principal
	service.input = input
	return service.created, service.createErr
}

func (service *fakeProviderProfileService) List(
	_ context.Context,
	principal auth.Principal,
) ([]providerprofile.Profile, error) {
	service.listPrincipal = principal
	return service.listed, service.listErr
}

func (service *fakeProviderProfileService) Update(
	_ context.Context,
	_ auth.Principal,
	profileID string,
	expectedVersion int64,
	input providerprofile.UpdateInput,
) (providerprofile.Profile, error) {
	service.updateID = profileID
	service.updateVersion = expectedVersion
	service.updateInput = input
	return service.updated, service.updateErr
}

func (service *fakeProviderProfileService) Health(
	_ context.Context,
	_ auth.Principal,
	profileID string,
) (providerprofile.Health, error) {
	service.healthID = profileID
	return service.health, service.healthErr
}

func (service *fakeProviderProfileService) Capabilities(auth.Principal) ([]asr.Capabilities, error) {
	return service.capabilities, service.capabilitiesErr
}

var _ ProviderProfileService = (*fakeProviderProfileService)(nil)
