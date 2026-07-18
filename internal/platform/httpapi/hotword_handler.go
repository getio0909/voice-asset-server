package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/hotword"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const maxHotwordBodyBytes = 160 * 1024

func (handler *Handler) handleHotwordSets(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
		return
	}
	if handler.hotwordService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "hotword service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, r.Method == http.MethodPost)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		sets, err := handler.hotwordService.List(r.Context(), principal)
		if err != nil {
			handler.writeHotwordError(w, r, requestID, err)
			return
		}
		handler.writeJSON(w, http.StatusOK, struct {
			Items []hotword.Set `json:"items"`
		}{Items: sets})
		return
	}
	var input hotword.CreateInput
	if !handler.decodeHotwordBody(w, r, requestID, &input) {
		return
	}
	created, err := handler.hotwordService.Create(r.Context(), principal, input)
	if err != nil {
		handler.writeHotwordError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/hotword-sets/"+created.ID)
	w.Header().Set("ETag", entityTag(created.ResourceVersion))
	handler.writeJSON(w, http.StatusCreated, created)
}

func (handler *Handler) handleHotwordSetRoute(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/hotword-sets/")
	parts := strings.Split(path, "/")
	setID, validID := identifier.NormalizeUUID(parts[0])
	versionRoute := len(parts) == 2 && parts[1] == "versions"
	if !validID || (len(parts) != 1 && !versionRoute) {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	wantedMethod := http.MethodPatch
	if versionRoute {
		wantedMethod = http.MethodPost
	}
	if r.Method != wantedMethod {
		handler.writeMethodNotAllowed(w, requestID, wantedMethod)
		return
	}
	if handler.hotwordService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "hotword service is unavailable", requestID)
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
	if versionRoute {
		var input hotword.AddVersionInput
		if !handler.decodeHotwordBody(w, r, requestID, &input) {
			return
		}
		updated, err := handler.hotwordService.AddVersion(
			r.Context(), principal, setID, expectedVersion, input,
		)
		if err != nil {
			handler.writeHotwordError(w, r, requestID, err)
			return
		}
		w.Header().Set("ETag", entityTag(updated.ResourceVersion))
		handler.writeJSON(w, http.StatusCreated, updated)
		return
	}
	var input hotword.UpdateInput
	if !handler.decodeHotwordBody(w, r, requestID, &input) {
		return
	}
	updated, err := handler.hotwordService.Update(
		r.Context(), principal, setID, expectedVersion, input,
	)
	if err != nil {
		handler.writeHotwordError(w, r, requestID, err)
		return
	}
	w.Header().Set("ETag", entityTag(updated.ResourceVersion))
	handler.writeJSON(w, http.StatusOK, updated)
}

func (handler *Handler) decodeHotwordBody(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	destination any,
) bool {
	if err := decodeJSONBody(w, r, destination, maxHotwordBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return false
		}
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return false
	}
	return true
}

func (handler *Handler) writeHotwordError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, hotword.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "hotword input is invalid", requestID)
	case errors.Is(err, hotword.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, hotword.ErrConflict):
		handler.writeError(w, http.StatusConflict, "hotword_conflict", "hotword set has changed or conflicts with an existing set", requestID)
	case errors.Is(err, hotword.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "hotword request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "hotword request could not be completed", requestID)
	}
}
