package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

func TestCreateWebSessionSetsProtectedCookie(t *testing.T) {
	expiresAt := time.Date(2026, 7, 16, 17, 0, 0, 0, time.UTC)
	refreshExpiresAt := expiresAt.Add(30 * 24 * time.Hour)
	authService := &fakeAuthService{loginResult: auth.LoginResult{
		AccessToken:      "va_test_token_with_sufficient_entropy",
		RefreshToken:     "va_rft_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		TokenType:        "Bearer",
		ExpiresAt:        expiresAt,
		RefreshExpiresAt: refreshExpiresAt,
		User: auth.Principal{
			UserID: "user-1", WorkspaceID: "workspace-1", Role: "owner", Email: "owner@example.com",
		},
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService,
		PublicOrigin: "https://voice.example.com", CookieSecure: true,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sessions",
		strings.NewReader(`{"email":"owner@example.com","password":"correct horse battery staple","device_name":"Firefox on Windows"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://voice.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies = %d, want 2", len(cookies))
	}
	cookie := cookieByName(t, cookies, sessionCookieName)
	if cookie.Name != sessionCookieName || cookie.Value != authService.loginResult.AccessToken ||
		!cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected session cookie: %+v", cookie)
	}
	refreshCookie := cookieByName(t, cookies, refreshCookieName)
	if refreshCookie.Value != authService.loginResult.RefreshToken || refreshCookie.Path != "/api/v1/auth" ||
		!refreshCookie.HttpOnly || !refreshCookie.Secure || refreshCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected refresh cookie: %+v", refreshCookie)
	}
	if authService.loginDeviceName != "Firefox on Windows" {
		t.Fatalf("device name = %q", authService.loginDeviceName)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, exposed := response["access_token"]; exposed {
		t.Fatal("web session response exposes access_token")
	}
	if _, exposed := response["refresh_token"]; exposed {
		t.Fatal("web session response exposes refresh_token")
	}
	if _, ok := response["user"]; !ok {
		t.Fatalf("response = %v, want user", response)
	}
}

func TestRefreshWebSessionRotatesProtectedCookies(t *testing.T) {
	expiresAt := time.Date(2026, 7, 16, 17, 0, 0, 0, time.UTC)
	authService := &fakeAuthService{refreshResult: auth.LoginResult{
		AccessToken:  "va_replacement_token_with_sufficient_entropy",
		RefreshToken: "va_rft_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		ExpiresAt:    expiresAt, RefreshExpiresAt: expiresAt.Add(20 * 24 * time.Hour),
		User: auth.Principal{UserID: "user-1", CredentialType: "session", CredentialID: "session-1"},
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService,
		PublicOrigin: "https://voice.example.com", CookieSecure: true,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session/refresh", nil)
	request.AddCookie(&http.Cookie{
		Name: refreshCookieName, Value: "va_rft_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	})
	request.Header.Set("Origin", "https://voice.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if authService.refreshToken != "va_rft_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("Refresh token = %q", authService.refreshToken)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 ||
		cookieByName(t, cookies, sessionCookieName).Value != authService.refreshResult.AccessToken ||
		cookieByName(t, cookies, refreshCookieName).Value != authService.refreshResult.RefreshToken {
		t.Fatalf("rotated cookies = %+v", cookies)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, accessExposed := response["access_token"]; accessExposed {
		t.Fatal("refresh response exposed access token")
	}
	if _, refreshExposed := response["refresh_token"]; refreshExposed {
		t.Fatal("refresh response exposed refresh token")
	}
}

func TestRefreshWebSessionRequiresExactOrigin(t *testing.T) {
	authService := &fakeAuthService{}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/session/refresh", nil)
	request.AddCookie(&http.Cookie{Name: refreshCookieName, Value: "va_rft_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"})
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || authService.refreshCalls != 0 {
		t.Fatalf("status/refresh calls = %d/%d", recorder.Code, authService.refreshCalls)
	}
}

func TestListAndRevokeDeviceSessions(t *testing.T) {
	currentID := "30000000-0000-4000-8000-000000000001"
	authService := &fakeAuthService{
		principal: auth.Principal{
			UserID:         "20000000-0000-4000-8000-000000000001",
			WorkspaceID:    "10000000-0000-4000-8000-000000000001",
			CredentialType: "session", CredentialID: currentID,
		},
		deviceSessions:       []auth.DeviceSession{{ID: currentID, DeviceName: "Firefox", Current: true}},
		revokedDeviceSession: auth.DeviceSession{ID: currentID, DeviceName: "Firefox", Current: true},
	}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, PublicOrigin: "https://voice.example.com",
	})
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/auth/device-sessions", nil)
	listRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	listRecorder := httptest.NewRecorder()

	handler.ServeHTTP(listRecorder, listRequest)

	if listRecorder.Code != http.StatusOK || authService.listDeviceCalls != 1 ||
		!strings.Contains(listRecorder.Body.String(), "Firefox") {
		t.Fatalf("list response/calls = %d %s / %d", listRecorder.Code, listRecorder.Body.String(), authService.listDeviceCalls)
	}

	revokeRequest := httptest.NewRequest(
		http.MethodDelete, "/api/v1/auth/device-sessions/"+currentID, nil,
	)
	revokeRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	revokeRequest.Header.Set("Origin", "https://voice.example.com")
	revokeRecorder := httptest.NewRecorder()

	handler.ServeHTTP(revokeRecorder, revokeRequest)

	if revokeRecorder.Code != http.StatusNoContent || authService.revokeDeviceID != currentID {
		t.Fatalf("revoke response/id = %d %s / %q", revokeRecorder.Code, revokeRecorder.Body.String(), authService.revokeDeviceID)
	}
	cookies := revokeRecorder.Result().Cookies()
	if len(cookies) != 2 || cookieByName(t, cookies, sessionCookieName).MaxAge >= 0 ||
		cookieByName(t, cookies, refreshCookieName).MaxAge >= 0 {
		t.Fatalf("cleared cookies = %+v", cookies)
	}
}

func TestCreatePairingSessionReturnsOneTimeVersionedPayload(t *testing.T) {
	pairingID := "40000000-0000-4000-8000-000000000001"
	secret := "va_pair_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	expiresAt := time.Date(2026, 7, 18, 4, 5, 0, 987_654_321, time.UTC)
	canonicalExpiresAt := expiresAt.Truncate(time.Second)
	authService := &fakeAuthService{
		principal: auth.Principal{
			UserID:         "20000000-0000-4000-8000-000000000001",
			WorkspaceID:    "10000000-0000-4000-8000-000000000001",
			CredentialType: "session", CredentialID: "30000000-0000-4000-8000-000000000001",
		},
		pairingSession: auth.PairingSession{ID: pairingID, Secret: secret, ExpiresAt: expiresAt},
	}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService,
		PublicOrigin: "https://voice.example.com", CookieSecure: true,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/pairing-sessions", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	request.Header.Set("Origin", "https://voice.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated || authService.createPairingCalls != 1 {
		t.Fatalf("status/create calls = %d/%d: %s", recorder.Code, authService.createPairingCalls, recorder.Body.String())
	}
	var response struct {
		ID        string    `json:"id"`
		ExpiresAt time.Time `json:"expires_at"`
		Payload   string    `json:"payload"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode pairing response: %v", err)
	}
	payload, err := url.Parse(response.Payload)
	if err != nil {
		t.Fatalf("parse pairing payload: %v", err)
	}
	query := payload.Query()
	if response.ID != pairingID || !response.ExpiresAt.Equal(canonicalExpiresAt) ||
		payload.Scheme != "voiceasset" || payload.Host != "pair" || payload.Path != "" ||
		query.Get("version") != "1" || query.Get("api_version") != "v1" ||
		query.Get("contract_version") != product.ContractVersion ||
		query.Get("origin") != "https://voice.example.com" ||
		query.Get("pairing_session_id") != pairingID || query.Get("secret") != secret ||
		query.Get("expires_at") != canonicalExpiresAt.Format(time.RFC3339) {
		t.Fatalf("pairing response/payload = %+v / %s", response, response.Payload)
	}
	// The secret may appear only inside the one-time payload, never as a reusable field.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil || raw["secret"] != nil {
		t.Fatalf("pairing response exposed a separate secret field: %s", recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
}

func TestClaimPairingSessionSetsProtectedCookiesAndRequiresExactOrigin(t *testing.T) {
	pairingID := "40000000-0000-4000-8000-000000000001"
	expiresAt := time.Date(2026, 7, 18, 16, 0, 0, 0, time.UTC)
	authService := &fakeAuthService{pairingClaimResult: auth.LoginResult{
		AccessToken:  "va_paired_token_with_sufficient_entropy",
		RefreshToken: "va_rft_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		ExpiresAt:    expiresAt, RefreshExpiresAt: expiresAt.Add(20 * 24 * time.Hour),
		User: auth.Principal{
			UserID:      "20000000-0000-4000-8000-000000000001",
			WorkspaceID: "10000000-0000-4000-8000-000000000001", Role: "owner",
		},
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService,
		PublicOrigin: "https://voice.example.com", CookieSecure: true,
	})
	body := `{"secret":"va_pair_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","device_name":"Pixel 9 Pro"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/pairing-sessions/"+pairingID+"/claim", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://voice.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated || authService.claimPairingCalls != 1 ||
		authService.claimPairingID != pairingID || authService.claimPairingDeviceName != "Pixel 9 Pro" ||
		authService.claimPairingSecret != "va_pair_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("claim response/calls = %d/%d/%q/%q: %s", recorder.Code, authService.claimPairingCalls,
			authService.claimPairingID, authService.claimPairingDeviceName, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 ||
		cookieByName(t, cookies, sessionCookieName).Value != authService.pairingClaimResult.AccessToken ||
		cookieByName(t, cookies, refreshCookieName).Value != authService.pairingClaimResult.RefreshToken {
		t.Fatalf("pairing cookies = %+v", cookies)
	}
	if strings.Contains(recorder.Body.String(), "access_token") || strings.Contains(recorder.Body.String(), "refresh_token") {
		t.Fatalf("pairing claim exposed credentials: %s", recorder.Body.String())
	}

	crossOrigin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/pairing-sessions/"+pairingID+"/claim", strings.NewReader(body))
	crossOrigin.Header.Set("Content-Type", "application/json")
	crossOrigin.Header.Set("Origin", "https://evil.example")
	crossOriginRecorder := httptest.NewRecorder()
	handler.ServeHTTP(crossOriginRecorder, crossOrigin)
	if crossOriginRecorder.Code != http.StatusForbidden || authService.claimPairingCalls != 1 {
		t.Fatalf("cross-origin status/calls = %d/%d", crossOriginRecorder.Code, authService.claimPairingCalls)
	}
}

func TestClaimPairingSessionUsesGenericRateLimitedFailure(t *testing.T) {
	authService := &fakeAuthService{pairingClaimErr: auth.ErrInvalidPairing}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, PublicOrigin: "https://voice.example.com",
	}).(*Handler)
	handler.now = func() time.Time { return time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC) }
	pairingID := "40000000-0000-4000-8000-000000000001"
	body := `{"secret":"va_pair_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","device_name":"Android"}`

	for attempt := 1; attempt <= 6; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/pairing-sessions/"+pairingID+"/claim", strings.NewReader(body))
		request.RemoteAddr = "192.0.2.10:54321"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://voice.example.com")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if attempt <= 5 && (recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), "invalid_pairing")) {
			t.Fatalf("attempt %d response = %d %s", attempt, recorder.Code, recorder.Body.String())
		}
		if attempt == 6 && (recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "") {
			t.Fatalf("attempt 6 response = %d %v", recorder.Code, recorder.Header())
		}
	}
}

func TestCreateWebSessionUsesGenericInvalidCredentialsError(t *testing.T) {
	authService := &fakeAuthService{loginErr: auth.ErrInvalidCredentials}
	handler := NewApplicationHandler(Options{BrandName: "VoiceAsset", AuthService: authService})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sessions",
		strings.NewReader(`{"email":"missing@example.com","password":"wrong"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	var response errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "invalid_credentials" || response.Error.Message != "email or password is incorrect" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestCreateWebSessionRateLimitsRepeatedFailures(t *testing.T) {
	authService := &fakeAuthService{loginErr: auth.ErrInvalidCredentials}
	handler := NewApplicationHandler(Options{BrandName: "VoiceAsset", AuthService: authService}).(*Handler)
	handler.now = func() time.Time { return time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC) }

	for attempt := 1; attempt <= 6; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sessions",
			strings.NewReader(`{"email":"owner@example.com","password":"wrong"}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if attempt <= 5 && recorder.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", attempt, recorder.Code, http.StatusUnauthorized)
		}
		if attempt == 6 {
			if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" {
				t.Fatalf("attempt 6 status/headers = %d/%v", recorder.Code, recorder.Header())
			}
		}
	}
}

func TestGetWebSessionAuthenticatesCookie(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Role: "owner", Email: "owner@example.com",
	}}
	handler := NewApplicationHandler(Options{BrandName: "VoiceAsset", AuthService: authService})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if authService.authenticateToken != "va_test_token_with_sufficient_entropy" {
		t.Fatalf("Authenticate token = %q", authService.authenticateToken)
	}
}

func TestDeleteWebSessionRejectsCrossOriginCookieRequest(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{UserID: "user-1"}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/session", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	request.Header.Set("Origin", "https://evil.example")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	if authService.logoutCalls != 0 {
		t.Fatalf("Logout calls = %d, want 0", authService.logoutCalls)
	}
}

func TestDeleteWebSessionRevokesAndClearsCookie(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{UserID: "user-1"}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/session", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	request.Header.Set("Origin", "https://voice.example.com")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	if authService.logoutCalls != 1 {
		t.Fatalf("Logout calls = %d, want 1", authService.logoutCalls)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 || cookieByName(t, cookies, sessionCookieName).MaxAge >= 0 ||
		cookieByName(t, cookies, refreshCookieName).MaxAge >= 0 {
		t.Fatalf("clear cookies = %+v", cookies)
	}
}

type fakeAuthService struct {
	loginResult            auth.LoginResult
	loginErr               error
	principal              auth.Principal
	authenticateErr        error
	authenticateToken      string
	logoutCalls            int
	logoutErr              error
	loginDeviceName        string
	refreshResult          auth.LoginResult
	refreshErr             error
	refreshToken           string
	refreshCalls           int
	deviceSessions         []auth.DeviceSession
	listDeviceCalls        int
	listDeviceErr          error
	revokedDeviceSession   auth.DeviceSession
	revokeDeviceID         string
	revokeDeviceErr        error
	pairingSession         auth.PairingSession
	createPairingCalls     int
	createPairingErr       error
	pairingClaimResult     auth.LoginResult
	pairingClaimErr        error
	claimPairingCalls      int
	claimPairingID         string
	claimPairingSecret     string
	claimPairingDeviceName string
}

func (f *fakeAuthService) Login(context.Context, string, string) (auth.LoginResult, error) {
	return f.loginResult, f.loginErr
}

func (f *fakeAuthService) LoginWithDevice(
	_ context.Context,
	_, _, deviceName string,
) (auth.LoginResult, error) {
	f.loginDeviceName = deviceName
	return f.loginResult, f.loginErr
}

func (f *fakeAuthService) Refresh(_ context.Context, token string) (auth.LoginResult, error) {
	f.refreshToken = token
	f.refreshCalls++
	return f.refreshResult, f.refreshErr
}

func (f *fakeAuthService) ListDeviceSessions(
	context.Context,
	auth.Principal,
) ([]auth.DeviceSession, error) {
	f.listDeviceCalls++
	return append([]auth.DeviceSession(nil), f.deviceSessions...), f.listDeviceErr
}

func (f *fakeAuthService) RevokeDeviceSession(
	_ context.Context,
	_ auth.Principal,
	sessionID string,
) (auth.DeviceSession, error) {
	f.revokeDeviceID = sessionID
	return f.revokedDeviceSession, f.revokeDeviceErr
}

func (f *fakeAuthService) CreatePairingSession(
	context.Context,
	auth.Principal,
) (auth.PairingSession, error) {
	f.createPairingCalls++
	return f.pairingSession, f.createPairingErr
}

func (f *fakeAuthService) ClaimPairingSession(
	_ context.Context,
	pairingID,
	secret,
	deviceName string,
) (auth.LoginResult, error) {
	f.claimPairingCalls++
	f.claimPairingID = pairingID
	f.claimPairingSecret = secret
	f.claimPairingDeviceName = deviceName
	return f.pairingClaimResult, f.pairingClaimErr
}

func (f *fakeAuthService) Authenticate(_ context.Context, token string) (auth.Principal, error) {
	f.authenticateToken = token
	return f.principal, f.authenticateErr
}

func (f *fakeAuthService) Logout(context.Context, string) error {
	f.logoutCalls++
	return f.logoutErr
}

var _ AuthService = (*fakeAuthService)(nil)

func cookieByName(t *testing.T, cookies []*http.Cookie, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q not found in %+v", name, cookies)
	return nil
}
