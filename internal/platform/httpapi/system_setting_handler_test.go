package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/systemsetting"
)

func TestAdminSystemSettingsReturnsAuditedAllowlistedProjection(t *testing.T) {
	auditService := &fakeAuditService{}
	handler := NewApplicationHandler(Options{
		AuthService: &fakeAuthService{principal: auth.Principal{
			UserID:      "20000000-0000-4000-8000-000000000001",
			WorkspaceID: "10000000-0000-4000-8000-000000000001",
			Scopes:      []string{auth.ScopeAdminRead},
		}},
		AuditService: auditService,
		SystemSettingService: systemsetting.NewService(systemsetting.Config{
			BrandName:                              "VoiceAsset Test",
			PublicOrigin:                           "https://voice.example.test",
			StorageBackend:                         "local",
			CookieSecure:                           true,
			ProviderCredentialEncryptionConfigured: true,
		}),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system-settings", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response systemsetting.Snapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.BrandName != "VoiceAsset Test" || response.Management != systemsetting.ManagementOperatorEnvironment {
		t.Fatalf("response = %+v", response)
	}
	if auditService.calls != 1 || auditService.input.Action != "admin.system_settings.read" ||
		auditService.input.TargetType != "system_settings" {
		t.Fatalf("audit = %+v, calls = %d", auditService.input, auditService.calls)
	}
}

func TestAdminSystemSettingsRejectsMutationMethodsBeforeAuthentication(t *testing.T) {
	handler := NewApplicationHandler(Options{
		SystemSettingService: systemsetting.NewService(systemsetting.Config{BrandName: "VoiceAsset"}),
	})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			request := httptest.NewRequest(method, "/api/v1/admin/system-settings", nil)
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
				t.Fatalf("status/Allow = %d/%q", recorder.Code, recorder.Header().Get("Allow"))
			}
		})
	}
}

func TestAdminSystemSettingsRequiresAdminRead(t *testing.T) {
	auditService := &fakeAuditService{}
	handler := NewApplicationHandler(Options{
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: "10000000-0000-4000-8000-000000000001",
			Scopes:      []string{auth.ScopeAssetsRead},
		}},
		AuditService:         auditService,
		SystemSettingService: systemsetting.NewService(systemsetting.Config{BrandName: "VoiceAsset"}),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system-settings", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || auditService.calls != 0 {
		t.Fatalf("status/audits = %d/%d: %s", recorder.Code, auditService.calls, recorder.Body.String())
	}
}

func TestAdminSystemSettingsRejectsQueryParameters(t *testing.T) {
	handler := NewApplicationHandler(Options{
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: "10000000-0000-4000-8000-000000000001",
			Scopes:      []string{auth.ScopeAdminRead},
		}},
		AuditService:         &fakeAuditService{},
		SystemSettingService: systemsetting.NewService(systemsetting.Config{BrandName: "VoiceAsset"}),
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system-settings?include=private", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}
