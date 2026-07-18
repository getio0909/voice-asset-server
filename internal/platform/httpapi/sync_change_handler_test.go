package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/syncchange"
)

func TestSyncChangesPassesCursorAndReturnsCheckpoint(t *testing.T) {
	service := &fakeSyncChangeService{result: syncchange.ListResult{
		Items: []syncchange.Change{{
			Sequence: 3, EntityType: "asset", EntityID: httpAssetID,
			Operation: "delete", EntityVersion: 4,
		}},
		NextCursor: "next-cursor", HasMore: true,
	}}
	handler := syncChangeHTTPHandler(service)
	request := authenticatedSyncChangeRequest(http.MethodGet, "/api/v1/sync/changes?limit=25&cursor=previous")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if service.input != (syncchange.ListInput{Limit: 25, Cursor: "previous"}) {
		t.Fatalf("input = %+v", service.input)
	}
	var response syncchange.ListResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil ||
		len(response.Items) != 1 || response.NextCursor != "next-cursor" || !response.HasMore {
		t.Fatalf("response = (%+v, %v)", response, err)
	}
}

func TestSyncChangesRejectsInvalidInputMethodAndScope(t *testing.T) {
	service := &fakeSyncChangeService{}
	handler := syncChangeHTTPHandler(service)
	for name, target := range map[string]string{
		"duplicate": "/api/v1/sync/changes?limit=1&limit=2",
		"unknown":   "/api/v1/sync/changes?secret=value",
		"limit":     "/api/v1/sync/changes?limit=nope",
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, authenticatedSyncChangeRequest(http.MethodGet, target))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedSyncChangeRequest(http.MethodPost, "/api/v1/sync/changes"))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d %q", recorder.Code, recorder.Header().Get("Allow"))
	}

	service.err = syncchange.ErrForbidden
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedSyncChangeRequest(http.MethodGet, "/api/v1/sync/changes"))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "assets:read") {
		t.Fatalf("scope response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestSyncChangesFailsClosedWhenServiceIsUnavailable(t *testing.T) {
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: "10000000-0000-4000-8000-000000000091",
			Scopes:      []string{auth.ScopeAssetsRead},
		}},
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedSyncChangeRequest(http.MethodGet, "/api/v1/sync/changes"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func syncChangeHTTPHandler(service SyncChangeService) http.Handler {
	return NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: "10000000-0000-4000-8000-000000000091",
			Scopes:      []string{auth.ScopeAssetsRead},
		}},
		SyncChangeService: service,
	})
}

func authenticatedSyncChangeRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	return request
}

type fakeSyncChangeService struct {
	result syncchange.ListResult
	err    error
	input  syncchange.ListInput
}

func (service *fakeSyncChangeService) List(
	_ context.Context,
	_ auth.Principal,
	input syncchange.ListInput,
) (syncchange.ListResult, error) {
	service.input = input
	return service.result, service.err
}

var _ SyncChangeService = (*fakeSyncChangeService)(nil)
