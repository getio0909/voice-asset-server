package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/getio0909/voice-asset-server/internal/account"
)

const maxPasswordChangeBodyBytes = 16 * 1024

func (handler *Handler) handleAccountPassword(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodPatch {
		handler.writeMethodNotAllowed(w, requestID, http.MethodPatch)
		return
	}
	if r.URL.RawQuery != "" {
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "password query parameters are not supported", requestID)
		return
	}
	if handler.accountService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "account service is unavailable", requestID)
		return
	}
	principal, authenticated := handler.authenticateRequest(w, r, requestID, true)
	if !authenticated {
		return
	}
	limitKey := principal.UserID + "|" + remoteIP(r.RemoteAddr)
	allowed, remaining, retryAfter := handler.passwordLimiter.Allow(limitKey, handler.now())
	w.Header().Set("RateLimit-Limit", strconv.Itoa(handler.passwordLimiter.limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		handler.writeError(w, http.StatusTooManyRequests, "rate_limited", "too many password change attempts", requestID)
		return
	}

	var input account.ChangePasswordInput
	if err := decodeJSONBody(w, r, &input, maxPasswordChangeBodyBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	if _, err := handler.accountService.ChangePassword(r.Context(), principal, input, requestID); err != nil {
		handler.writeAccountPasswordError(w, r, requestID, err)
		return
	}
	handler.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) writeAccountPasswordError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, account.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "password input is invalid", requestID)
	case errors.Is(err, account.ErrInvalidCredentials):
		handler.writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect", requestID)
	case errors.Is(err, account.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "password change is not permitted", requestID)
	case errors.Is(err, account.ErrCredentialsChanged):
		handler.writeError(w, http.StatusConflict, "credentials_changed", "account credentials changed; sign in again", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "password change failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "password change could not be completed", requestID)
	}
}
