package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/clip"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const maxClipBodyBytes = 16 * 1024

func (handler *Handler) handleCreateAudioClip(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
) {
	if r.Method != http.MethodPost {
		handler.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if handler.clipService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "clip service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	var input clip.CreateInput
	if err := decodeJSONBody(w, r, &input, maxClipBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	created, err := handler.clipService.Create(r.Context(), principal, assetID, input, requestID)
	if err != nil {
		handler.writeClipError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", created.DownloadURL)
	handler.writeJSON(w, http.StatusCreated, created)
}

func (handler *Handler) handleAudioClip(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodHead)
		return
	}
	clipID := strings.TrimPrefix(r.URL.Path, "/api/v1/audio-clips/")
	clipID, validID := identifier.NormalizeUUID(clipID)
	if !validID || strings.Contains(clipID, "/") {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if handler.clipService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "clip service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	media, err := handler.clipService.Open(r.Context(), principal, clipID)
	if err != nil {
		handler.writeClipError(w, r, requestID, err)
		return
	}
	defer media.Content.Close()
	if !handler.recordReadAudit(w, r, requestID, principal, "audio_clip.read", "audio_clip", clipID, nil) {
		return
	}
	etag := strconv.Quote(media.SHA256)
	w.Header().Set("ETag", etag)
	if value := r.Header.Get("If-Match"); value != "" && !etagListMatches(value, etag, false) {
		w.Header().Del("Content-Type")
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if value := r.Header.Get("If-None-Match"); value != "" && etagListMatches(value, etag, true) {
		w.Header().Del("Content-Type")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	r.Header.Del("If-Match")
	r.Header.Del("If-None-Match")
	if strings.Contains(r.Header.Get("Range"), ",") {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(media.Size, 10))
		w.Header().Del("Content-Type")
		http.Error(w, "multiple byte ranges are not supported", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	w.Header().Set("Content-Type", media.MIMEType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+clipID+`.wav"`)
	http.ServeContent(audioContentWriter{ResponseWriter: w}, r, clipID, time.Time{}, media.Content)
}

func (handler *Handler) writeClipError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, clip.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "clip input is invalid", requestID)
	case errors.Is(err, clip.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "required scopes are missing", requestID)
	case errors.Is(err, clip.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, clip.ErrUnavailable):
		handler.writeError(w, http.StatusServiceUnavailable, "clip_unavailable", "audio clip could not be generated", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "clip request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "clip request could not be completed", requestID)
	}
}
