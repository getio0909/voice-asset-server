package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/account"
	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestPatchAccountPasswordAuthenticatesChangesAndClearsCookies(t *testing.T) {
	accountService := &fakeAccountService{result: account.ChangePasswordResult{RevokedSessions: 2}}
	handler := passwordHTTPHandler(accountService, nil)
	request := passwordRequest(`{"current_password":"current-password","new_password":"new-password-456"}`)
	request.Header.Set("X-Request-ID", "request-1")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
	}
	if accountService.calls != 1 || accountService.input.CurrentPassword != "current-password" ||
		accountService.input.NewPassword != "new-password-456" || accountService.requestID != "request-1" ||
		accountService.principal.CredentialType != "session" {
		t.Fatalf("service call = %+v", accountService)
	}
	if recorder.Header().Get("RateLimit-Limit") != "5" {
		t.Fatalf("rate-limit headers = %v", recorder.Header())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 || cookieByName(t, cookies, sessionCookieName).MaxAge >= 0 ||
		cookieByName(t, cookies, refreshCookieName).MaxAge >= 0 {
		t.Fatalf("cleared cookies = %+v", cookies)
	}
}

func TestPatchAccountPasswordRejectsCrossOriginCookieRequest(t *testing.T) {
	accountService := &fakeAccountService{}
	handler := passwordHTTPHandler(accountService, nil)
	request := passwordRequest(`{"current_password":"current-password","new_password":"new-password-456"}`)
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || accountService.calls != 0 {
		t.Fatalf("response/calls = %d/%d: %s", recorder.Code, accountService.calls, recorder.Body.String())
	}
}

func TestPatchAccountPasswordMapsExpectedErrors(t *testing.T) {
	tests := []struct {
		err  error
		code int
		body string
	}{
		{err: account.ErrInvalidInput, code: http.StatusBadRequest, body: "invalid_request"},
		{err: account.ErrInvalidCredentials, code: http.StatusUnauthorized, body: "invalid_credentials"},
		{err: account.ErrForbidden, code: http.StatusForbidden, body: "forbidden"},
		{err: account.ErrCredentialsChanged, code: http.StatusConflict, body: "credentials_changed"},
	}
	for _, test := range tests {
		service := &fakeAccountService{err: test.err}
		handler := passwordHTTPHandler(service, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, passwordRequest(`{"current_password":"current-password","new_password":"new-password-456"}`))
		if recorder.Code != test.code || !strings.Contains(recorder.Body.String(), test.body) {
			t.Fatalf("error %v response = %d %s", test.err, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPatchAccountPasswordBoundsBodyAndAttempts(t *testing.T) {
	service := &fakeAccountService{err: account.ErrInvalidCredentials}
	handler := passwordHTTPHandler(service, nil)
	oversized := passwordRequest(`{"current_password":"` + strings.Repeat("a", maxPasswordChangeBodyBytes) + `","new_password":"new-password-456"}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, oversized)
	if recorder.Code != http.StatusRequestEntityTooLarge || service.calls != 0 {
		t.Fatalf("oversized response/calls = %d/%d", recorder.Code, service.calls)
	}

	for attempt := 1; attempt <= 6; attempt++ {
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, passwordRequest(`{"current_password":"wrong-password","new_password":"new-password-456"}`))
		if attempt < 5 && recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", attempt, recorder.Code)
		}
		if attempt == 5 && recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status = %d, want 429", attempt, recorder.Code)
		}
		if attempt == 6 && recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status = %d, want 429", attempt, recorder.Code)
		}
	}
}

func TestPatchAccountPasswordDoesNotLogPasswordValues(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	service := &fakeAccountService{err: errors.New("database unavailable")}
	handler := passwordHTTPHandler(service, logger)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, passwordRequest(`{"current_password":"secret-current-123","new_password":"secret-future-456"}`))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	if strings.Contains(output.String(), "secret-current-123") || strings.Contains(output.String(), "secret-future-456") {
		t.Fatalf("password leaked to logs: %s", output.String())
	}
}

func passwordHTTPHandler(service AccountService, logger *slog.Logger) http.Handler {
	principal := auth.Principal{
		UserID:         "20000000-0000-4000-8000-000000000001",
		WorkspaceID:    "10000000-0000-4000-8000-000000000001",
		CredentialType: "session", CredentialID: "30000000-0000-4000-8000-000000000001",
	}
	return NewApplicationHandler(Options{
		BrandName: "VoiceAsset", Logger: logger,
		AuthService: &fakeAuthService{principal: principal}, AccountService: service,
		PublicOrigin: "https://voice.example.com",
	})
}

func passwordRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/auth/password", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	return request
}

type fakeAccountService struct {
	result    account.ChangePasswordResult
	err       error
	principal auth.Principal
	input     account.ChangePasswordInput
	requestID string
	calls     int
}

func (service *fakeAccountService) ChangePassword(
	_ context.Context,
	principal auth.Principal,
	input account.ChangePasswordInput,
	requestID string,
) (account.ChangePasswordResult, error) {
	service.calls++
	service.principal = principal
	service.input = input
	service.requestID = requestID
	return service.result, service.err
}

var _ AccountService = (*fakeAccountService)(nil)
