package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

const (
	sessionCookieName = "voiceasset_session"
	refreshCookieName = "voiceasset_refresh"
	maxLoginBodyBytes = 16 * 1024
)

type loginRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name,omitempty"`
}

type webSessionResponse struct {
	ExpiresAt        time.Time      `json:"expires_at"`
	RefreshExpiresAt time.Time      `json:"refresh_expires_at"`
	User             auth.Principal `json:"user"`
}

type currentSessionResponse struct {
	User auth.Principal `json:"user"`
}

type deviceSessionListResponse struct {
	Items []auth.DeviceSession `json:"items"`
}

type pairingSessionResponse struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
	Payload   string    `json:"payload"`
}

type pairingClaimRequest struct {
	Secret     string `json:"secret"`
	DeviceName string `json:"device_name"`
}

type deviceLoginService interface {
	LoginWithDevice(context.Context, string, string, string) (auth.LoginResult, error)
}

type refreshSessionService interface {
	Refresh(context.Context, string) (auth.LoginResult, error)
}

type deviceSessionService interface {
	ListDeviceSessions(context.Context, auth.Principal) ([]auth.DeviceSession, error)
	RevokeDeviceSession(context.Context, auth.Principal, string) (auth.DeviceSession, error)
}

type pairingService interface {
	CreatePairingSession(context.Context, auth.Principal) (auth.PairingSession, error)
	ClaimPairingSession(context.Context, string, string, string) (auth.LoginResult, error)
}

func (h *Handler) handlePairingSessions(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	service, ok := h.authService.(pairingService)
	if h.authService == nil || !ok {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "device pairing is unavailable", requestID)
		return
	}
	principal, authenticated := h.authenticateRequest(w, r, requestID, true)
	if !authenticated {
		return
	}
	pairing, err := service.CreatePairingSession(r.Context(), principal)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			h.writeUnauthorized(w, requestID)
			return
		}
		h.logger.ErrorContext(r.Context(), "create pairing session", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "pairing session could not be created", requestID)
		return
	}
	canonicalExpiresAt := pairing.ExpiresAt.UTC().Truncate(time.Second)
	query := url.Values{
		"api_version":        {product.APIVersion},
		"contract_version":   {product.ContractVersion},
		"expires_at":         {canonicalExpiresAt.Format(time.RFC3339)},
		"origin":             {h.publicOrigin},
		"pairing_session_id": {pairing.ID},
		"secret":             {pairing.Secret},
		"version":            {"1"},
	}
	payload := (&url.URL{Scheme: "voiceasset", Host: "pair", RawQuery: query.Encode()}).String()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", "/api/v1/auth/pairing-sessions/"+pairing.ID)
	h.writeJSON(w, http.StatusCreated, pairingSessionResponse{
		ID: pairing.ID, ExpiresAt: canonicalExpiresAt, Payload: payload,
	})
}

func (h *Handler) handlePairingSessionRoute(w http.ResponseWriter, r *http.Request, requestID string) {
	const prefix = "/api/v1/auth/pairing-sessions/"
	resource := strings.TrimPrefix(r.URL.Path, prefix)
	pairingID, hasClaimSuffix := strings.CutSuffix(resource, "/claim")
	if !hasClaimSuffix {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	pairingID, validID := identifier.NormalizeUUID(pairingID)
	if !validID {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	service, ok := h.authService.(pairingService)
	if h.authService == nil || !ok {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "device pairing is unavailable", requestID)
		return
	}
	if r.Header.Get("Origin") != h.publicOrigin {
		h.writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed", requestID)
		return
	}
	clientIP := remoteIP(r.RemoteAddr)
	allowed, remaining, retryAfter := h.pairingLimiter.Allow(clientIP, h.now())
	w.Header().Set("RateLimit-Limit", strconv.Itoa(h.pairingLimiter.limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		h.writeError(w, http.StatusTooManyRequests, "rate_limited", "too many pairing attempts", requestID)
		return
	}
	var input pairingClaimRequest
	if err := decodeJSONBody(w, r, &input, maxLoginBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			h.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		h.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	result, err := service.ClaimPairingSession(r.Context(), pairingID, input.Secret, input.DeviceName)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidPairing) {
			h.writeError(w, http.StatusUnauthorized, "invalid_pairing", "pairing session is invalid or unavailable", requestID)
			return
		}
		h.logger.ErrorContext(r.Context(), "claim pairing session", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "pairing session could not be claimed", requestID)
		return
	}
	h.pairingLimiter.Reset(clientIP)
	h.setSessionCookies(w, result)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", "/api/v1/auth/session")
	h.writeJSON(w, http.StatusCreated, webSessionResponse{
		ExpiresAt: result.ExpiresAt, RefreshExpiresAt: result.RefreshExpiresAt, User: result.User,
	})
}

func (h *Handler) handleAuthSessions(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.authService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "authentication is unavailable", requestID)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != h.publicOrigin {
		h.writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed", requestID)
		return
	}
	clientIP := remoteIP(r.RemoteAddr)
	allowed, remaining, retryAfter := h.loginLimiter.Allow(clientIP, h.now())
	w.Header().Set("RateLimit-Limit", strconv.Itoa(h.loginLimiter.limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		h.writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts", requestID)
		return
	}

	var input loginRequest
	if err := decodeJSONBody(w, r, &input, maxLoginBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			h.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		h.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	var (
		result auth.LoginResult
		err    error
	)
	if service, ok := h.authService.(deviceLoginService); ok {
		result, err = service.LoginWithDevice(r.Context(), input.Email, input.Password, input.DeviceName)
	} else {
		result, err = h.authService.Login(r.Context(), input.Email, input.Password)
	}
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			h.writeError(w, http.StatusUnauthorized, "invalid_credentials", "email or password is incorrect", requestID)
			return
		}
		if errors.Is(err, auth.ErrInvalidDeviceName) {
			h.writeError(w, http.StatusBadRequest, "invalid_request", "device name is invalid", requestID)
			return
		}
		h.logger.ErrorContext(r.Context(), "create web session", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "authentication could not be completed", requestID)
		return
	}
	h.loginLimiter.Reset(clientIP)
	h.setSessionCookies(w, result)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", "/api/v1/auth/session")
	h.writeJSON(w, http.StatusCreated, webSessionResponse{
		ExpiresAt: result.ExpiresAt, RefreshExpiresAt: result.RefreshExpiresAt, User: result.User,
	})
}

func (h *Handler) handleAuthSessionRefresh(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	service, ok := h.authService.(refreshSessionService)
	if h.authService == nil || !ok {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "session refresh is unavailable", requestID)
		return
	}
	if r.Header.Get("Origin") != h.publicOrigin {
		h.writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed", requestID)
		return
	}
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil || cookie.Value == "" {
		h.clearSessionCookies(w)
		h.writeUnauthorized(w, requestID)
		return
	}
	result, err := service.Refresh(r.Context(), cookie.Value)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			h.clearSessionCookies(w)
			h.writeUnauthorized(w, requestID)
			return
		}
		h.logger.ErrorContext(r.Context(), "refresh web session", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "session could not be refreshed", requestID)
		return
	}
	h.setSessionCookies(w, result)
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, http.StatusOK, webSessionResponse{
		ExpiresAt: result.ExpiresAt, RefreshExpiresAt: result.RefreshExpiresAt, User: result.User,
	})
}

func (h *Handler) handleDeviceSessions(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	service, ok := h.authService.(deviceSessionService)
	if h.authService == nil || !ok {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "device sessions are unavailable", requestID)
		return
	}
	principal, authenticated := h.authenticateRequest(w, r, requestID, false)
	if !authenticated {
		return
	}
	sessions, err := service.ListDeviceSessions(r.Context(), principal)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			h.writeUnauthorized(w, requestID)
			return
		}
		h.logger.ErrorContext(r.Context(), "list device sessions", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "device sessions could not be listed", requestID)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, http.StatusOK, deviceSessionListResponse{Items: sessions})
}

func (h *Handler) handleDeviceSessionRoute(w http.ResponseWriter, r *http.Request, requestID string) {
	sessionID, ok := identifier.NormalizeUUID(strings.TrimPrefix(r.URL.Path, "/api/v1/auth/device-sessions/"))
	if !ok {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if r.Method != http.MethodDelete {
		h.writeMethodNotAllowed(w, requestID, http.MethodDelete)
		return
	}
	service, available := h.authService.(deviceSessionService)
	if h.authService == nil || !available {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "device sessions are unavailable", requestID)
		return
	}
	principal, authenticated := h.authenticateRequest(w, r, requestID, true)
	if !authenticated {
		return
	}
	revoked, err := service.RevokeDeviceSession(r.Context(), principal, sessionID)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrUnauthorized):
			h.writeUnauthorized(w, requestID)
		case errors.Is(err, auth.ErrDeviceSessionNotFound):
			h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		default:
			h.logger.ErrorContext(r.Context(), "revoke device session", "error", err, "request_id", requestID)
			h.writeError(w, http.StatusInternalServerError, "internal_error", "device session could not be revoked", requestID)
		}
		return
	}
	if revoked.Current {
		h.clearSessionCookies(w)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleAuthSession(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		h.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodDelete)
		return
	}
	if h.authService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "authentication is unavailable", requestID)
		return
	}
	token, cookieAuthenticated, err := sessionToken(r)
	if err != nil {
		h.writeUnauthorized(w, requestID)
		return
	}
	if r.Method == http.MethodDelete && cookieAuthenticated && r.Header.Get("Origin") != h.publicOrigin {
		h.writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed", requestID)
		return
	}
	principal, err := h.authService.Authenticate(r.Context(), token)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthorized) {
			h.logger.ErrorContext(r.Context(), "authenticate session", "error", err, "request_id", requestID)
		}
		h.writeUnauthorized(w, requestID)
		return
	}
	if r.Method == http.MethodGet {
		h.writeJSON(w, http.StatusOK, currentSessionResponse{User: principal})
		return
	}
	if err := h.authService.Logout(r.Context(), token); err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			h.writeUnauthorized(w, requestID)
			return
		}
		h.logger.ErrorContext(r.Context(), "revoke session", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "session could not be revoked", requestID)
		return
	}
	h.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, destination any, limit int64) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return fmt.Errorf("content type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body must contain one JSON value")
	}
	return nil
}

func sessionToken(r *http.Request) (token string, cookieAuthenticated bool, err error) {
	var bearer string
	if authorization := strings.TrimSpace(r.Header.Get("Authorization")); authorization != "" {
		parts := strings.Fields(authorization)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return "", false, fmt.Errorf("invalid authorization header")
		}
		bearer = parts[1]
	}
	cookie, cookieErr := r.Cookie(sessionCookieName)
	if bearer != "" && cookieErr == nil && cookie.Value != bearer {
		return "", false, fmt.Errorf("ambiguous session credentials")
	}
	if bearer != "" {
		return bearer, false, nil
	}
	if cookieErr != nil || cookie.Value == "" {
		return "", false, fmt.Errorf("session credential is missing")
	}
	return cookie.Value, true, nil
}

func (h *Handler) setSessionCookies(w http.ResponseWriter, result auth.LoginResult) {
	h.setProtectedCookie(w, sessionCookieName, result.AccessToken, "/api/v1", result.ExpiresAt)
	if result.RefreshToken != "" {
		h.setProtectedCookie(
			w, refreshCookieName, result.RefreshToken, "/api/v1/auth", result.RefreshExpiresAt,
		)
	}
}

func (h *Handler) setProtectedCookie(
	w http.ResponseWriter,
	name,
	value,
	path string,
	expiresAt time.Time,
) {
	maxAge := int(expiresAt.Sub(h.now().UTC()).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: path, Expires: expiresAt, MaxAge: maxAge,
		HttpOnly: true, Secure: h.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) clearSessionCookies(w http.ResponseWriter) {
	h.clearProtectedCookie(w, sessionCookieName, "/api/v1")
	h.clearProtectedCookie(w, refreshCookieName, "/api/v1/auth")
}

func (h *Handler) clearProtectedCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: path, Expires: time.Unix(1, 0), MaxAge: -1,
		HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) writeUnauthorized(w http.ResponseWriter, requestID string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="voiceasset"`)
	h.writeError(w, http.StatusUnauthorized, "unauthorized", "authentication is required", requestID)
}

func (h *Handler) authenticateRequest(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	unsafe bool,
) (auth.Principal, bool) {
	if h.authService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "authentication is unavailable", requestID)
		return auth.Principal{}, false
	}
	token, cookieAuthenticated, err := sessionToken(r)
	if err != nil {
		h.writeUnauthorized(w, requestID)
		return auth.Principal{}, false
	}
	if unsafe && cookieAuthenticated && r.Header.Get("Origin") != h.publicOrigin {
		h.writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed", requestID)
		return auth.Principal{}, false
	}
	principal, err := h.authService.Authenticate(r.Context(), token)
	if err != nil {
		if !errors.Is(err, auth.ErrUnauthorized) {
			h.logger.ErrorContext(r.Context(), "authenticate request", "error", err, "request_id", requestID)
		}
		h.writeUnauthorized(w, requestID)
		return auth.Principal{}, false
	}
	return principal, true
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}
