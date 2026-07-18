package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/transcriptexport"
)

const maxTranscriptExportBodyBytes = 8 * 1024

func (handler *Handler) handleCreateTranscriptExport(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	revisionID string,
) {
	if r.Method != http.MethodPost {
		handler.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if handler.exportService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "transcript export service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	var input transcriptexport.CreateInput
	if err := decodeJSONBody(w, r, &input, maxTranscriptExportBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	created, err := handler.exportService.Create(r.Context(), principal, revisionID, input, requestID)
	if err != nil {
		handler.writeTranscriptExportError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", created.DownloadURL)
	handler.writeJSON(w, http.StatusCreated, created)
}

func (handler *Handler) handleTranscriptExport(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodHead)
		return
	}
	exportID := strings.TrimPrefix(r.URL.Path, "/api/v1/transcript-exports/")
	exportID, validID := identifier.NormalizeUUID(exportID)
	if !validID || strings.Contains(exportID, "/") {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if handler.exportService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "transcript export service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	media, err := handler.exportService.Open(r.Context(), principal, exportID)
	if err != nil {
		handler.writeTranscriptExportError(w, r, requestID, err)
		return
	}
	defer media.Content.Close()
	if !handler.recordReadAudit(w, r, requestID, principal, "transcript_export.read", "transcript_export", exportID, nil) {
		return
	}
	etag := strconv.Quote(media.SHA256)
	w.Header().Set("ETag", etag)
	if value := r.Header.Get("If-None-Match"); value != "" && etagListMatches(value, etag, true) {
		w.Header().Del("Content-Type")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	r.Header.Del("If-None-Match")
	if strings.Contains(r.Header.Get("Range"), ",") {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(media.Size, 10))
		w.Header().Del("Content-Type")
		http.Error(w, "multiple byte ranges are not supported", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	w.Header().Set("Content-Type", media.MIMEType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportID+`.`+media.Extension+`"`)
	http.ServeContent(audioContentWriter{ResponseWriter: w}, r, exportID, time.Time{}, media.Content)
}

func (handler *Handler) writeTranscriptExportError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, transcriptexport.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "transcript export input is invalid", requestID)
	case errors.Is(err, transcriptexport.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "required scopes are missing", requestID)
	case errors.Is(err, transcriptexport.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, transcriptexport.ErrUnavailable):
		handler.writeError(w, http.StatusServiceUnavailable, "export_unavailable", "transcript export could not be generated", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "transcript export request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "transcript export could not be completed", requestID)
	}
}
