package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/apikey"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const maxAPIKeyBodyBytes = 32 * 1024

type apiKeyListResponse struct {
	Items []apikey.APIKey `json:"items"`
}

func (handler *Handler) handleAPIKeys(w http.ResponseWriter, r *http.Request, requestID string) {
	if handler.apiKeyService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "API key service is unavailable", requestID)
		return
	}
	switch r.Method {
	case http.MethodGet:
		principal, ok := handler.authenticateRequest(w, r, requestID, false)
		if !ok {
			return
		}
		results, err := handler.apiKeyService.List(r.Context(), principal)
		if err != nil {
			handler.writeAPIKeyError(w, r, requestID, err)
			return
		}
		if !handler.recordReadAudit(w, r, requestID, principal, "api_key.listed", "api_key_collection", "", map[string]any{
			"result_count": len(results),
		}) {
			return
		}
		handler.writeJSON(w, http.StatusOK, apiKeyListResponse{Items: results})
	case http.MethodPost:
		principal, ok := handler.authenticateRequest(w, r, requestID, true)
		if !ok {
			return
		}
		var input apikey.CreateInput
		if err := decodeJSONBody(w, r, &input, maxAPIKeyBodyBytes); err != nil {
			handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
			return
		}
		result, err := handler.apiKeyService.Create(r.Context(), principal, input, requestID)
		if err != nil {
			handler.writeAPIKeyError(w, r, requestID, err)
			return
		}
		w.Header().Set("Location", "/api/v1/api-keys/"+result.APIKey.ID)
		handler.writeJSON(w, http.StatusCreated, result)
	default:
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
	}
}

func (handler *Handler) handleAPIKeyRoute(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodDelete {
		handler.writeMethodNotAllowed(w, requestID, http.MethodDelete)
		return
	}
	keyID := strings.TrimPrefix(r.URL.Path, "/api/v1/api-keys/")
	keyID, validID := identifier.NormalizeUUID(keyID)
	if !validID || strings.Contains(keyID, "/") {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if handler.apiKeyService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "API key service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	result, err := handler.apiKeyService.Revoke(r.Context(), principal, keyID, requestID)
	if err != nil {
		handler.writeAPIKeyError(w, r, requestID, err)
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func (handler *Handler) writeAPIKeyError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, apikey.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "API key input is invalid", requestID)
	case errors.Is(err, apikey.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, apikey.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "API key request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "API key request could not be completed", requestID)
	}
}
