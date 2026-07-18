package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestRealtimeEndpointAuthenticatesAndRequiresWriteScope(t *testing.T) {
	endpoint := &fakeRealtimeEndpoint{}
	principal := auth.Principal{
		WorkspaceID: "workspace-1", UserID: "user-1",
		Scopes: []string{auth.ScopeTranscriptionsWrite},
	}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: &fakeAuthService{principal: principal},
		RealtimeEndpoint: endpoint,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/realtime/transcriptions", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || endpoint.calls != 1 ||
		endpoint.principal.WorkspaceID != principal.WorkspaceID || endpoint.principal.UserID != principal.UserID {
		t.Fatalf("response=%d calls=%d principal=%+v", recorder.Code, endpoint.calls, endpoint.principal)
	}
	viewerEndpoint := &fakeRealtimeEndpoint{}
	viewerHandler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: "workspace-1", UserID: "viewer-1",
			Scopes: []string{auth.ScopeTranscriptsRead},
		}},
		RealtimeEndpoint: viewerEndpoint,
	})
	viewerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/realtime/transcriptions", nil)
	viewerRequest.Header.Set("Authorization", "Bearer va_viewer_token_with_sufficient_entropy")
	viewerRecorder := httptest.NewRecorder()

	viewerHandler.ServeHTTP(viewerRecorder, viewerRequest)

	if viewerRecorder.Code != http.StatusForbidden || viewerEndpoint.calls != 0 {
		t.Fatalf("viewer response=%d calls=%d", viewerRecorder.Code, viewerEndpoint.calls)
	}
}

func TestRealtimeEndpointRejectsUnsafeOrUnavailableUpgrade(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		auth       string
		origin     string
		endpoint   RealtimeEndpoint
		wantStatus int
	}{
		{name: "method", method: http.MethodPost, endpoint: &fakeRealtimeEndpoint{}, wantStatus: http.StatusMethodNotAllowed},
		{name: "unavailable", method: http.MethodGet, auth: "bearer", wantStatus: http.StatusServiceUnavailable},
		{name: "unauthenticated", method: http.MethodGet, endpoint: &fakeRealtimeEndpoint{}, wantStatus: http.StatusUnauthorized},
		{name: "cookie origin", method: http.MethodGet, auth: "cookie", origin: "https://evil.example", endpoint: &fakeRealtimeEndpoint{}, wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewApplicationHandler(Options{
				BrandName: "VoiceAsset", PublicOrigin: "https://voice.example",
				AuthService: &fakeAuthService{principal: auth.Principal{
					WorkspaceID: "workspace-1", UserID: "user-1",
					Scopes: []string{auth.ScopeTranscriptionsWrite},
				}},
				RealtimeEndpoint: test.endpoint,
			})
			request := httptest.NewRequest(test.method, "/api/v1/realtime/transcriptions", nil)
			switch test.auth {
			case "bearer":
				request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
			case "cookie":
				request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_cookie_token_with_sufficient_entropy"})
				request.Header.Set("Origin", test.origin)
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status=%d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}

type fakeRealtimeEndpoint struct {
	calls     int
	principal auth.Principal
}

func (endpoint *fakeRealtimeEndpoint) Serve(
	_ context.Context,
	principal auth.Principal,
	w http.ResponseWriter,
	_ *http.Request,
) {
	endpoint.calls++
	endpoint.principal = principal
	w.WriteHeader(http.StatusNoContent)
}
