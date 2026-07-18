package httpapi

import (
	"errors"
	"net/http"

	"github.com/getio0909/voice-asset-server/internal/systemsetting"
)

func (handler *Handler) handleAdminSystemSettings(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if len(r.URL.Query()) != 0 {
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "system settings do not accept query parameters", requestID)
		return
	}
	if handler.systemSettingService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "system settings service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	result, err := handler.systemSettingService.Get(r.Context(), principal)
	if err != nil {
		handler.writeSystemSettingError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(w, r, requestID, principal, "admin.system_settings.read", "system_settings", "", nil) {
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func (handler *Handler) writeSystemSettingError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	if errors.Is(err, systemsetting.ErrForbidden) {
		handler.writeError(w, http.StatusForbidden, "forbidden", "admin:read scope is required", requestID)
		return
	}
	handler.logger.ErrorContext(r.Context(), "system settings request failed", "error", err, "request_id", requestID)
	handler.writeError(w, http.StatusInternalServerError, "internal_error", "system settings request could not be completed", requestID)
}
