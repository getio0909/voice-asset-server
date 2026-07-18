package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/llmprofile"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const maxLLMProfileBodyBytes = 128 * 1024

func (handler *Handler) handleLLMProfiles(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
		return
	}
	if handler.llmProfileService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "LLM profile service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, r.Method == http.MethodPost)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		profiles, err := handler.llmProfileService.List(r.Context(), principal)
		if err != nil {
			handler.writeLLMProfileError(w, r, requestID, err)
			return
		}
		handler.writeJSON(w, http.StatusOK, struct {
			Items []llmprofile.Profile `json:"items"`
		}{profiles})
		return
	}
	var input llmprofile.CreateInput
	if !handler.decodeLLMProfileBody(w, r, requestID, &input) {
		return
	}
	created, err := handler.llmProfileService.Create(r.Context(), principal, input)
	if err != nil {
		handler.writeLLMProfileError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/llm-profiles/"+created.ID)
	w.Header().Set("ETag", entityTag(created.Version))
	handler.writeJSON(w, http.StatusCreated, created)
}

func (handler *Handler) handleLLMProfileRoute(w http.ResponseWriter, r *http.Request, requestID string) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/llm-profiles/")
	parts := strings.Split(path, "/")
	profileID, validID := identifier.NormalizeUUID(parts[0])
	healthRoute := len(parts) == 2 && parts[1] == "health"
	if !validID || (len(parts) != 1 && !healthRoute) {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if handler.llmProfileService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "LLM profile service is unavailable", requestID)
		return
	}
	if healthRoute {
		if r.Method != http.MethodPost {
			handler.writeMethodNotAllowed(w, requestID, http.MethodPost)
			return
		}
		principal, ok := handler.authenticateRequest(w, r, requestID, true)
		if !ok {
			return
		}
		result, err := handler.llmProfileService.Health(r.Context(), principal, profileID)
		if err != nil {
			handler.writeLLMProfileError(w, r, requestID, err)
			return
		}
		handler.writeJSON(w, http.StatusOK, result)
		return
	}
	if r.Method != http.MethodPatch {
		handler.writeMethodNotAllowed(w, requestID, http.MethodPatch)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	expected, present, valid := parseEntityVersion(r.Header.Get("If-Match"))
	if !present {
		handler.writeError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match is required", requestID)
		return
	}
	if !valid {
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "If-Match must contain one resource version", requestID)
		return
	}
	var input llmprofile.UpdateInput
	if !handler.decodeLLMProfileBody(w, r, requestID, &input) {
		return
	}
	updated, err := handler.llmProfileService.Update(r.Context(), principal, profileID, expected, input)
	if err != nil {
		handler.writeLLMProfileError(w, r, requestID, err)
		return
	}
	w.Header().Set("ETag", entityTag(updated.Version))
	handler.writeJSON(w, http.StatusOK, updated)
}

func (handler *Handler) handleLLMProviderCapabilities(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.llmProfileService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "LLM profile service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	capabilities, err := handler.llmProfileService.Capabilities(principal)
	if err != nil {
		handler.writeLLMProfileError(w, r, requestID, err)
		return
	}
	handler.writeJSON(w, http.StatusOK, struct {
		Items []llm.Capabilities `json:"items"`
	}{capabilities})
}

func (handler *Handler) decodeLLMProfileBody(w http.ResponseWriter, r *http.Request, requestID string, destination any) bool {
	if err := decodeJSONBody(w, r, destination, maxLLMProfileBodyBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return false
		}
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return false
	}
	return true
}

func (handler *Handler) writeLLMProfileError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, llmprofile.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "LLM profile input is invalid", requestID)
	case errors.Is(err, llmprofile.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, llmprofile.ErrConflict):
		handler.writeError(w, http.StatusConflict, "profile_conflict", "LLM profile conflicts with an existing profile", requestID)
	case errors.Is(err, llmprofile.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, llmprofile.ErrEncryptionUnavailable):
		handler.writeError(w, http.StatusServiceUnavailable, "encryption_unavailable", "provider credential encryption is not configured", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "LLM profile request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "LLM profile request could not be completed", requestID)
	}
}
