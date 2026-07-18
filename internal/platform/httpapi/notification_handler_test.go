package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/notification"
)

const (
	httpNotificationWorkspaceID = "10000000-0000-4000-8000-0000000000a1"
	httpNotificationUserID      = "20000000-0000-4000-8000-0000000000a1"
)

func TestEventsPassCursorAndPersistSafeReadAudit(t *testing.T) {
	service := &fakeNotificationService{result: notification.ListResult{
		Items: []notification.Event{{
			Sequence: 4, ID: "30000000-0000-4000-8000-0000000000a1",
			Type: notification.TypeJobFailed, JobID: "40000000-0000-4000-8000-0000000000a1",
			JobKind: "mock_transcribe", State: notification.StateFailed,
			OccurredAt: time.Date(2026, 7, 18, 8, 30, 0, 0, time.UTC),
		}},
		NextCursor: "next-cursor", HasMore: true,
	}}
	auditService := &fakeAuditService{}
	handler := notificationHTTPHandler(service, auditService)
	request := authenticatedNotificationRequest(http.MethodGet, "/api/v1/events?limit=25&cursor=previous")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if service.input != (notification.ListInput{Limit: 25, Cursor: "previous"}) {
		t.Fatalf("input = %+v", service.input)
	}
	var response notification.ListResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil ||
		len(response.Items) != 1 || response.NextCursor != "next-cursor" || !response.HasMore {
		t.Fatalf("response = (%+v, %v)", response, err)
	}
	if auditService.calls != 1 || auditService.input.Action != "notification.listed" ||
		auditService.input.TargetType != "notification_collection" ||
		auditService.input.Metadata["result_count"] != 1 || auditService.input.Metadata["has_more"] != true {
		t.Fatalf("audit = calls:%d input:%+v", auditService.calls, auditService.input)
	}
}

func TestEventsRejectInvalidInputMethodAndAuthorization(t *testing.T) {
	service := &fakeNotificationService{}
	handler := notificationHTTPHandler(service, nil)
	for name, target := range map[string]string{
		"duplicate": "/api/v1/events?limit=1&limit=2",
		"unknown":   "/api/v1/events?secret=value",
		"limit":     "/api/v1/events?limit=nope",
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, authenticatedNotificationRequest(http.MethodGet, target))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedNotificationRequest(http.MethodPost, "/api/v1/events"))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d %q", recorder.Code, recorder.Header().Get("Allow"))
	}

	service.err = notification.ErrForbidden
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedNotificationRequest(http.MethodGet, "/api/v1/events"))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "interactive session") {
		t.Fatalf("authorization response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestEventsFailClosedWhenServiceIsUnavailable(t *testing.T) {
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: httpNotificationWorkspaceID, UserID: httpNotificationUserID,
			CredentialType: "session", Scopes: []string{auth.ScopeTranscriptsRead},
		}},
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedNotificationRequest(http.MethodGet, "/api/v1/events"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func notificationHTTPHandler(service NotificationService, auditService AuditService) http.Handler {
	return NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: httpNotificationWorkspaceID, UserID: httpNotificationUserID,
			CredentialType: "session", Scopes: []string{auth.ScopeTranscriptsRead},
		}},
		NotificationService: service,
		AuditService:        auditService,
	})
}

func authenticatedNotificationRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	return request
}

type fakeNotificationService struct {
	result notification.ListResult
	err    error
	input  notification.ListInput
}

func (service *fakeNotificationService) List(
	_ context.Context,
	_ auth.Principal,
	input notification.ListInput,
) (notification.ListResult, error) {
	service.input = input
	return service.result, service.err
}

var _ NotificationService = (*fakeNotificationService)(nil)
