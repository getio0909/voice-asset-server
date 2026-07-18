package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/providerprofile"
)

const maxProviderProfileBodyBytes = 96 * 1024

func (handler *Handler) handleProviderProfiles(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
		return
	}
	if handler.providerService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "provider profile service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, r.Method == http.MethodPost)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		profiles, err := handler.providerService.List(r.Context(), principal)
		if err != nil {
			handler.writeProviderProfileError(w, r, requestID, err)
			return
		}
		handler.writeJSON(w, http.StatusOK, struct {
			Items []providerprofile.Profile `json:"items"`
		}{Items: profiles})
		return
	}

	var input providerprofile.CreateInput
	if err := decodeJSONBody(w, r, &input, maxProviderProfileBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	created, err := handler.providerService.Create(r.Context(), principal, input)
	if err != nil {
		handler.writeProviderProfileError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/provider-profiles/"+created.ID)
	w.Header().Set("ETag", entityTag(created.Version))
	handler.writeJSON(w, http.StatusCreated, created)
}

func (handler *Handler) handleProviderProfileRoute(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/provider-profiles/")
	parts := strings.Split(path, "/")
	profileID, validID := identifier.NormalizeUUID(parts[0])
	if !validID || (len(parts) != 1 && (len(parts) != 2 || parts[1] != "health")) {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if handler.providerService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "provider profile service is unavailable", requestID)
		return
	}
	if len(parts) == 2 {
		if r.Method != http.MethodPost {
			handler.writeMethodNotAllowed(w, requestID, http.MethodPost)
			return
		}
		principal, ok := handler.authenticateRequest(w, r, requestID, true)
		if !ok {
			return
		}
		result, err := handler.providerService.Health(r.Context(), principal, profileID)
		if err != nil {
			handler.writeProviderProfileError(w, r, requestID, err)
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
	expectedVersion, present, valid := parseEntityVersion(r.Header.Get("If-Match"))
	if !present {
		handler.writeError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match is required", requestID)
		return
	}
	if !valid {
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "If-Match must contain one resource version", requestID)
		return
	}
	var input providerprofile.UpdateInput
	if err := decodeJSONBody(w, r, &input, maxProviderProfileBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	updated, err := handler.providerService.Update(
		r.Context(), principal, profileID, expectedVersion, input,
	)
	if err != nil {
		handler.writeProviderProfileError(w, r, requestID, err)
		return
	}
	w.Header().Set("ETag", entityTag(updated.Version))
	handler.writeJSON(w, http.StatusOK, updated)
}

func (handler *Handler) handleProviderCapabilities(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.providerService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "provider profile service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	capabilities, err := handler.providerService.Capabilities(principal)
	if err != nil {
		handler.writeProviderProfileError(w, r, requestID, err)
		return
	}
	handler.writeJSON(w, http.StatusOK, struct {
		Items []asr.Capabilities `json:"items"`
	}{Items: capabilities})
}

func parseEntityVersion(value string) (version int64, present, valid bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false, false
	}
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' || strings.Contains(value[1:len(value)-1], `"`) {
		return 0, true, false
	}
	parsed, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	return parsed, true, err == nil && parsed > 0
}

func (handler *Handler) writeProviderProfileError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, providerprofile.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "provider profile input is invalid", requestID)
	case errors.Is(err, providerprofile.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, providerprofile.ErrConflict):
		handler.writeError(w, http.StatusConflict, "profile_conflict", "provider profile conflicts with an existing profile", requestID)
	case errors.Is(err, providerprofile.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, providerprofile.ErrEncryptionUnavailable):
		handler.writeError(w, http.StatusServiceUnavailable, "encryption_unavailable", "provider credential encryption is not configured", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "provider profile request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "provider profile request could not be completed", requestID)
	}
}
