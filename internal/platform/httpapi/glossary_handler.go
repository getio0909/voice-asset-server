package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/glossary"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const maxGlossaryBodyBytes = 640 * 1024

func (handler *Handler) handleGlossarySets(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
		return
	}
	if handler.glossaryService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "glossary service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, r.Method == http.MethodPost)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		sets, err := handler.glossaryService.List(r.Context(), principal)
		if err != nil {
			handler.writeGlossaryError(w, r, requestID, err)
			return
		}
		handler.writeJSON(w, http.StatusOK, struct {
			Items []glossary.Set `json:"items"`
		}{sets})
		return
	}
	var input glossary.CreateInput
	if !handler.decodeGlossaryBody(w, r, requestID, &input) {
		return
	}
	created, err := handler.glossaryService.Create(r.Context(), principal, input)
	if err != nil {
		handler.writeGlossaryError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/glossary-sets/"+created.ID)
	w.Header().Set("ETag", entityTag(created.ResourceVersion))
	handler.writeJSON(w, http.StatusCreated, created)
}

func (handler *Handler) handleGlossarySetRoute(w http.ResponseWriter, r *http.Request, requestID string) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/glossary-sets/")
	parts := strings.Split(path, "/")
	setID, validID := identifier.NormalizeUUID(parts[0])
	versionRoute := len(parts) == 2 && parts[1] == "versions"
	if !validID || (len(parts) != 1 && !versionRoute) {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	wanted := http.MethodPatch
	if versionRoute {
		wanted = http.MethodPost
	}
	if r.Method != wanted {
		handler.writeMethodNotAllowed(w, requestID, wanted)
		return
	}
	if handler.glossaryService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "glossary service is unavailable", requestID)
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
	if versionRoute {
		var input glossary.AddVersionInput
		if !handler.decodeGlossaryBody(w, r, requestID, &input) {
			return
		}
		updated, err := handler.glossaryService.AddVersion(r.Context(), principal, setID, expected, input)
		if err != nil {
			handler.writeGlossaryError(w, r, requestID, err)
			return
		}
		w.Header().Set("ETag", entityTag(updated.ResourceVersion))
		handler.writeJSON(w, http.StatusCreated, updated)
		return
	}
	var input glossary.UpdateInput
	if !handler.decodeGlossaryBody(w, r, requestID, &input) {
		return
	}
	updated, err := handler.glossaryService.Update(r.Context(), principal, setID, expected, input)
	if err != nil {
		handler.writeGlossaryError(w, r, requestID, err)
		return
	}
	w.Header().Set("ETag", entityTag(updated.ResourceVersion))
	handler.writeJSON(w, http.StatusOK, updated)
}

func (handler *Handler) decodeGlossaryBody(w http.ResponseWriter, r *http.Request, requestID string, destination any) bool {
	if err := decodeJSONBody(w, r, destination, maxGlossaryBodyBytes); err != nil {
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

func (handler *Handler) writeGlossaryError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, glossary.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "glossary input is invalid", requestID)
	case errors.Is(err, glossary.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, glossary.ErrConflict):
		handler.writeError(w, http.StatusConflict, "glossary_conflict", "glossary set has changed or conflicts with an existing set", requestID)
	case errors.Is(err, glossary.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "glossary request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "glossary request could not be completed", requestID)
	}
}
