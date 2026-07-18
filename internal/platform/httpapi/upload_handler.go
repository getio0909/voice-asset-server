package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/upload"
)

const maxUploadBodyBytes = 64 * 1024

func (h *Handler) handleUploads(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.uploadService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "upload service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	var input upload.CreateInput
	if err := decodeJSONBody(w, r, &input, maxUploadBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			h.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		h.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	created, replayed, err := h.uploadService.Create(r.Context(), principal, input, r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.writeUploadError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/uploads/"+created.ID)
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	h.writeJSON(w, status, created)
}

func (h *Handler) handleUploadRoute(w http.ResponseWriter, r *http.Request, requestID string) {
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/uploads/"), "/")
	if len(segments) == 0 {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	canonicalID, validID := identifier.NormalizeUUID(segments[0])
	if !validID {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	segments[0] = canonicalID
	if len(segments) == 1 && segments[0] != "" {
		h.handleUpload(w, r, requestID, segments[0])
		return
	}
	if len(segments) == 3 && segments[0] != "" && segments[1] == "parts" {
		partNumber, err := strconv.Atoi(segments[2])
		if err != nil || partNumber < 1 || partNumber > 10000 {
			h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
			return
		}
		h.handleUploadPart(w, r, requestID, segments[0], partNumber)
		return
	}
	if len(segments) == 2 && segments[0] != "" && segments[1] == "complete" {
		h.handleUploadComplete(w, r, requestID, segments[0])
		return
	}
	h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
}

func (h *Handler) handleUploadComplete(w http.ResponseWriter, r *http.Request, requestID, uploadID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.uploadService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "upload service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	completed, replayed, err := h.uploadService.Complete(r.Context(), principal, uploadID)
	if err != nil {
		h.writeUploadError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/assets/"+completed.AssetID)
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	h.writeJSON(w, http.StatusOK, completed)
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request, requestID, uploadID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if h.uploadService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "upload service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	session, err := h.uploadService.Get(r.Context(), principal, uploadID)
	if err != nil {
		h.writeUploadError(w, r, requestID, err)
		return
	}
	h.writeJSON(w, http.StatusOK, session)
}

func (h *Handler) handleUploadPart(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	uploadID string,
	partNumber int,
) {
	if r.Method != http.MethodPut {
		h.writeMethodNotAllowed(w, requestID, http.MethodPut)
		return
	}
	if h.uploadService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "upload service is unavailable", requestID)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/octet-stream" {
		h.writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "upload parts require application/octet-stream", requestID)
		return
	}
	if r.ContentLength > upload.DefaultPartSize {
		h.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "upload part is too large", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	part, replayed, err := h.uploadService.PutPart(
		r.Context(), principal, uploadID, partNumber, r.Header.Get("X-Part-SHA256"), r.Body,
	)
	if err != nil {
		h.writeUploadError(w, r, requestID, err)
		return
	}
	w.Header().Set("ETag", `"`+part.SHA256+`"`)
	w.Header().Set("Location", "/api/v1/uploads/"+uploadID+"/parts/"+strconv.Itoa(part.Number))
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	h.writeJSON(w, status, part)
}

func (h *Handler) writeUploadError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, upload.ErrPartTooLarge):
		h.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "upload part is too large", requestID)
	case errors.Is(err, upload.ErrInvalidInput), errors.Is(err, upload.ErrInvalidPart):
		h.writeError(w, http.StatusBadRequest, "invalid_request", "upload input is invalid", requestID)
	case errors.Is(err, upload.ErrForbidden):
		h.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, upload.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, upload.ErrExpired):
		h.writeError(w, http.StatusGone, "upload_expired", "upload session has expired", requestID)
	case errors.Is(err, upload.ErrIdempotencyConflict):
		h.writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used for another request", requestID)
	case errors.Is(err, upload.ErrStateConflict), errors.Is(err, upload.ErrPartConflict):
		h.writeError(w, http.StatusConflict, "upload_conflict", "upload state conflicts with this request", requestID)
	case errors.Is(err, upload.ErrChecksumMismatch):
		h.writeError(w, http.StatusUnprocessableEntity, "checksum_mismatch", "upload checksum does not match", requestID)
	case errors.Is(err, upload.ErrIncomplete):
		h.writeError(w, http.StatusConflict, "upload_incomplete", "all declared upload parts are required", requestID)
	case errors.Is(err, upload.ErrUnsupportedMedia):
		h.writeError(w, http.StatusUnsupportedMediaType, "unsupported_media", "uploaded file is not supported audio", requestID)
	default:
		h.logger.ErrorContext(r.Context(), "upload request failed", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "upload request could not be completed", requestID)
	}
}
