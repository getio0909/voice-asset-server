package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/webhook"
)

const httpWebhookID = "40000000-0000-4000-8000-0000000000c1"

func TestWebhookRoutesExposeOwnerLifecycleAndOneTimeSecrets(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID:      "20000000-0000-4000-8000-0000000000c1",
		WorkspaceID: "10000000-0000-4000-8000-0000000000c1",
		Role:        "owner", CredentialType: "session",
		Scopes: []string{auth.ScopeAdminRead, auth.ScopeAdminWrite},
	}}
	service := &fakeWebhookService{
		created: webhook.CreateResult{Endpoint: webhook.Endpoint{
			ID: httpWebhookID, WorkspaceID: authService.principal.WorkspaceID,
			DisplayName: "Receiver", URL: "https://hooks.example.com/events",
			EventTypes: []string{webhook.EventJobSucceeded}, State: webhook.StateEnabled,
			Version: 1, SecretConfigured: true,
		}, SigningSecret: "va_whsec_fixture_secret"},
		updated:      webhook.Endpoint{ID: httpWebhookID, Version: 2, State: webhook.StateDisabled},
		rotated:      webhook.CreateResult{Endpoint: webhook.Endpoint{ID: httpWebhookID, Version: 3, SecretConfigured: true}, SigningSecret: "va_whsec_rotated"},
		testDelivery: webhook.Delivery{ID: "50000000-0000-4000-8000-0000000000c1", State: webhook.DeliveryPending},
	}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, WebhookService: service,
		PublicOrigin: "https://voice.example.com",
	})

	create := httptest.NewRequest(http.MethodPost, "/api/v1/admin/webhooks", strings.NewReader(`{
		"display_name":"Receiver","url":"https://hooks.example.com/events",
		"event_types":["job.succeeded"],"state":"enabled"}`))
	create.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("X-Request-ID", "webhook-create")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, create)
	if recorder.Code != http.StatusCreated ||
		recorder.Header().Get("Location") != "/api/v1/admin/webhooks/"+httpWebhookID ||
		recorder.Header().Get("ETag") != `"1"` ||
		!strings.Contains(recorder.Body.String(), service.created.SigningSecret) {
		t.Fatalf("create response = %d, headers=%v, body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if service.createRequestID != "webhook-create" || service.createInput.DisplayName != "Receiver" {
		t.Fatalf("create service call = %+v / %q", service.createInput, service.createRequestID)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v1/admin/webhooks", nil)
	list.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, list)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"items"`) {
		t.Fatalf("list response = %d: %s", recorder.Code, recorder.Body.String())
	}

	update := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/webhooks/"+httpWebhookID, strings.NewReader(`{"state":"disabled"}`))
	update.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	update.Header.Set("Content-Type", "application/json")
	update.Header.Set("If-Match", `"1"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, update)
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"2"` || service.updateVersion != 1 {
		t.Fatalf("update response = %d, headers=%v, service=%+v", recorder.Code, recorder.Header(), service)
	}

	rotate := httptest.NewRequest(http.MethodPost, "/api/v1/admin/webhooks/"+httpWebhookID+"/rotate-secret", nil)
	rotate.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	rotate.Header.Set("If-Match", `"2"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, rotate)
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"3"` ||
		!strings.Contains(recorder.Body.String(), service.rotated.SigningSecret) {
		t.Fatalf("rotate response = %d, headers=%v, body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	testRequest := httptest.NewRequest(http.MethodPost, "/api/v1/admin/webhooks/"+httpWebhookID+"/test", nil)
	testRequest.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, testRequest)
	if recorder.Code != http.StatusAccepted || service.testEndpointID != httpWebhookID {
		t.Fatalf("test response = %d, service=%+v, body=%s", recorder.Code, service, recorder.Body.String())
	}
}

func TestWebhookRoutesRequirePreconditionsAndMapSafeErrors(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{Scopes: []string{auth.ScopeAdminWrite}}}
	service := &fakeWebhookService{updateErr: webhook.ErrConflict}
	handler := NewApplicationHandler(Options{BrandName: "VoiceAsset", AuthService: authService, WebhookService: service})

	missingETag := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/webhooks/"+httpWebhookID, strings.NewReader(`{"state":"enabled"}`))
	missingETag.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, missingETag)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing ETag status = %d", recorder.Code)
	}

	conflict := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/webhooks/"+httpWebhookID, strings.NewReader(`{"state":"enabled"}`))
	conflict.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	conflict.Header.Set("Content-Type", "application/json")
	conflict.Header.Set("If-Match", `"1"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, conflict)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"webhook_conflict"`) {
		t.Fatalf("conflict response = %d: %s", recorder.Code, recorder.Body.String())
	}

	badID := httptest.NewRequest(http.MethodGet, "/api/v1/admin/webhooks/not-a-uuid", nil)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, badID)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("bad ID status = %d", recorder.Code)
	}
}

type fakeWebhookService struct {
	created         webhook.CreateResult
	listed          []webhook.Endpoint
	updated         webhook.Endpoint
	rotated         webhook.CreateResult
	testDelivery    webhook.Delivery
	createInput     webhook.CreateInput
	createRequestID string
	updateVersion   int64
	testEndpointID  string
	createErr       error
	listErr         error
	updateErr       error
	rotateErr       error
	testErr         error
}

func (service *fakeWebhookService) Create(_ context.Context, _ auth.Principal, input webhook.CreateInput, requestID string) (webhook.CreateResult, error) {
	service.createInput = input
	service.createRequestID = requestID
	return service.created, service.createErr
}

func (service *fakeWebhookService) List(context.Context, auth.Principal) ([]webhook.Endpoint, error) {
	return append([]webhook.Endpoint(nil), service.listed...), service.listErr
}

func (service *fakeWebhookService) ListDeliveries(context.Context, auth.Principal, string, int) ([]webhook.Delivery, error) {
	return nil, nil
}

func (service *fakeWebhookService) Update(_ context.Context, _ auth.Principal, _ string, version int64, _ webhook.UpdateInput, _ string) (webhook.Endpoint, error) {
	service.updateVersion = version
	return service.updated, service.updateErr
}

func (service *fakeWebhookService) RotateSecret(context.Context, auth.Principal, string, int64, string) (webhook.CreateResult, error) {
	return service.rotated, service.rotateErr
}

func (service *fakeWebhookService) EnqueueTest(_ context.Context, _ auth.Principal, endpointID, _ string) (webhook.Delivery, error) {
	service.testEndpointID = endpointID
	return service.testDelivery, service.testErr
}

var _ WebhookService = (*fakeWebhookService)(nil)
