package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/webhook"
)

const maxWebhookBodyBytes = 16 * 1024

func (handler *Handler) handleAdminWebhooks(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
) {
	if r.URL.RawQuery != "" {
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "webhook query parameters are not supported", requestID)
		return
	}
	if handler.webhookService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "webhook service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, r.Method != http.MethodGet)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := handler.webhookService.List(r.Context(), principal)
		if err != nil {
			handler.writeWebhookError(w, r, requestID, err)
			return
		}
		handler.writeJSON(w, http.StatusOK, struct {
			Items []webhook.Endpoint `json:"items"`
		}{Items: items})
	case http.MethodPost:
		var input webhook.CreateInput
		if err := decodeJSONBody(w, r, &input, maxWebhookBodyBytes); err != nil {
			handler.writeBodyDecodeError(w, requestID, err)
			return
		}
		created, err := handler.webhookService.Create(r.Context(), principal, input, requestID)
		if err != nil {
			handler.writeWebhookError(w, r, requestID, err)
			return
		}
		w.Header().Set("Location", "/api/v1/admin/webhooks/"+created.ID)
		w.Header().Set("ETag", entityTag(created.Version))
		handler.writeJSON(w, http.StatusCreated, created)
	default:
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
	}
}

func (handler *Handler) handleAdminWebhookRoute(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/webhooks/")
	parts := strings.Split(path, "/")
	endpointID, validID := identifier.NormalizeUUID(parts[0])
	if !validID || (len(parts) != 1 && len(parts) != 2) {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if handler.webhookService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "webhook service is unavailable", requestID)
		return
	}
	if len(parts) == 2 && parts[1] == "deliveries" {
		if r.Method != http.MethodGet {
			handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
			return
		}
		values := r.URL.Query()
		if !singleQueryValues(values, "limit") {
			handler.writeError(w, http.StatusBadRequest, "invalid_request", "duplicate or unsupported query parameters", requestID)
			return
		}
		limit, err := parseOptionalLimit(values.Get("limit"))
		if err != nil || limit < 0 || limit > 100 {
			handler.writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100", requestID)
			return
		}
		principal, ok := handler.authenticateRequest(w, r, requestID, false)
		if !ok {
			return
		}
		items, err := handler.webhookService.ListDeliveries(r.Context(), principal, endpointID, limit)
		if err != nil {
			handler.writeWebhookError(w, r, requestID, err)
			return
		}
		handler.writeJSON(w, http.StatusOK, struct {
			Items []webhook.Delivery `json:"items"`
		}{Items: items})
		return
	}
	if len(parts) == 2 && parts[1] == "test" {
		if r.Method != http.MethodPost {
			handler.writeMethodNotAllowed(w, requestID, http.MethodPost)
			return
		}
		if r.URL.RawQuery != "" {
			handler.writeError(w, http.StatusBadRequest, "invalid_request", "webhook query parameters are not supported", requestID)
			return
		}
		principal, ok := handler.authenticateRequest(w, r, requestID, true)
		if !ok {
			return
		}
		result, err := handler.webhookService.EnqueueTest(r.Context(), principal, endpointID, requestID)
		if err != nil {
			handler.writeWebhookError(w, r, requestID, err)
			return
		}
		handler.writeJSON(w, http.StatusAccepted, result)
		return
	}
	if len(parts) == 2 && parts[1] == "rotate-secret" {
		if r.Method != http.MethodPost {
			handler.writeMethodNotAllowed(w, requestID, http.MethodPost)
			return
		}
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
		result, err := handler.webhookService.RotateSecret(r.Context(), principal, endpointID, expectedVersion, requestID)
		if err != nil {
			handler.writeWebhookError(w, r, requestID, err)
			return
		}
		w.Header().Set("ETag", entityTag(result.Version))
		handler.writeJSON(w, http.StatusOK, result)
		return
	}
	if len(parts) != 1 || r.Method != http.MethodPatch {
		handler.writeMethodNotAllowed(w, requestID, http.MethodPatch)
		return
	}
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
	var input webhook.UpdateInput
	if err := decodeJSONBody(w, r, &input, maxWebhookBodyBytes); err != nil {
		handler.writeBodyDecodeError(w, requestID, err)
		return
	}
	updated, err := handler.webhookService.Update(r.Context(), principal, endpointID, expectedVersion, input, requestID)
	if err != nil {
		handler.writeWebhookError(w, r, requestID, err)
		return
	}
	w.Header().Set("ETag", entityTag(updated.Version))
	handler.writeJSON(w, http.StatusOK, updated)
}

func (handler *Handler) writeBodyDecodeError(w http.ResponseWriter, requestID string, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
		return
	}
	handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
}

func (handler *Handler) writeWebhookError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, webhook.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "webhook input is invalid", requestID)
	case errors.Is(err, webhook.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "webhook administration is not permitted", requestID)
	case errors.Is(err, webhook.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, webhook.ErrConflict):
		handler.writeError(w, http.StatusConflict, "webhook_conflict", "webhook changed or is disabled", requestID)
	case errors.Is(err, webhook.ErrEncryptionUnavailable):
		handler.writeError(w, http.StatusServiceUnavailable, "encryption_unavailable", "webhook secret encryption is not configured", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "webhook request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "webhook request could not be completed", requestID)
	}
}
