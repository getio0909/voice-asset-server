package httpapi

import (
	"errors"
	"net/http"

	"github.com/getio0909/voice-asset-server/internal/workspace"
)

const maxWorkspaceBodyBytes = 4 * 1024

func (handler *Handler) handleAdminWorkspace(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.URL.RawQuery != "" {
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "workspace query parameters are not supported", requestID)
		return
	}
	if handler.workspaceService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "workspace service is unavailable", requestID)
		return
	}
	switch r.Method {
	case http.MethodGet:
		principal, ok := handler.authenticateRequest(w, r, requestID, false)
		if !ok {
			return
		}
		result, err := handler.workspaceService.Get(r.Context(), principal)
		if err != nil {
			handler.writeWorkspaceError(w, r, requestID, err)
			return
		}
		if !handler.recordReadAudit(w, r, requestID, principal, "workspace.read", "workspace", result.ID, map[string]any{
			"version": result.Version,
		}) {
			return
		}
		w.Header().Set("ETag", entityTag(result.Version))
		handler.writeJSON(w, http.StatusOK, result)
	case http.MethodPatch:
		expectedVersion, present, valid := parseEntityVersion(r.Header.Get("If-Match"))
		if !present {
			handler.writeError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match is required", requestID)
			return
		}
		if !valid {
			handler.writeError(w, http.StatusBadRequest, "invalid_request", "If-Match must contain one resource version", requestID)
			return
		}
		principal, ok := handler.authenticateRequest(w, r, requestID, true)
		if !ok {
			return
		}
		var input workspace.UpdateInput
		if err := decodeJSONBody(w, r, &input, maxWorkspaceBodyBytes); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
				return
			}
			handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
			return
		}
		result, err := handler.workspaceService.Update(r.Context(), principal, expectedVersion, input, requestID)
		if err != nil {
			handler.writeWorkspaceError(w, r, requestID, err)
			return
		}
		w.Header().Set("ETag", entityTag(result.Version))
		handler.writeJSON(w, http.StatusOK, result)
	default:
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPatch)
	}
}

func (handler *Handler) writeWorkspaceError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, workspace.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "workspace input is invalid", requestID)
	case errors.Is(err, workspace.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "workspace administration is not permitted", requestID)
	case errors.Is(err, workspace.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, workspace.ErrVersionConflict):
		handler.writeError(w, http.StatusPreconditionFailed, "version_conflict", "workspace version changed", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "workspace request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "workspace request could not be completed", requestID)
	}
}
