package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/waveform"
)

func (handler *Handler) handleAssetWaveform(
	w http.ResponseWriter,
	r *http.Request,
	requestID, assetID string,
) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodHead)
		return
	}
	if handler.waveformService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "waveform service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	media, err := handler.waveformService.Open(r.Context(), principal, assetID)
	if err != nil {
		handler.writeWaveformError(w, r, requestID, err)
		return
	}
	defer media.Content.Close()
	if !handler.recordReadAudit(w, r, requestID, principal, "waveform.read", "asset_waveform", media.ObjectID, map[string]any{
		"asset_id": media.AssetID,
	}) {
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
	w.Header().Set("Content-Disposition", `inline; filename="`+media.AssetID+`.png"`)
	http.ServeContent(audioContentWriter{ResponseWriter: w}, r, media.AssetID, time.Time{}, media.Content)
}

func (handler *Handler) writeWaveformError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, waveform.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, waveform.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, waveform.ErrUnavailable):
		handler.writeError(w, http.StatusServiceUnavailable, "waveform_unavailable", "waveform is unavailable", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "waveform request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "waveform request could not be completed", requestID)
	}
}
