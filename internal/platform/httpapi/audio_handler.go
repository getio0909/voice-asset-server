package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/audio"
)

func (h *Handler) handleAssetAudio(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodHead)
		return
	}
	if h.audioService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "audio service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	media, err := h.audioService.Open(r.Context(), principal, assetID)
	if err != nil {
		h.writeAudioError(w, r, requestID, err)
		return
	}
	defer func() {
		if err := media.Content.Close(); err != nil {
			h.logger.WarnContext(r.Context(), "close audio response", "error", err, "request_id", requestID)
		}
	}()
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
	// Preconditions are handled above so ServeContent cannot produce an error
	// response after the representation Content-Type has been selected.
	r.Header.Del("If-Match")
	r.Header.Del("If-None-Match")
	if strings.Contains(r.Header.Get("Range"), ",") {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(media.Size, 10))
		w.Header().Del("Content-Type")
		http.Error(w, "multiple byte ranges are not supported", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	w.Header().Set("Content-Type", media.MIMEType)
	http.ServeContent(audioContentWriter{ResponseWriter: w}, r, media.AssetID, time.Time{}, media.Content)
}

type audioContentWriter struct {
	http.ResponseWriter
}

func (w audioContentWriter) WriteHeader(status int) {
	if status >= http.StatusBadRequest {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.ResponseWriter.WriteHeader(status)
}

func etagListMatches(value, current string, weak bool) bool {
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		if weak {
			candidate = strings.TrimPrefix(candidate, "W/")
		}
		if candidate == current && (weak || !strings.HasPrefix(candidate, "W/")) {
			return true
		}
	}
	return false
}

func (h *Handler) writeAudioError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, audio.ErrAudioForbidden):
		h.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, audio.ErrAudioNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	default:
		h.logger.ErrorContext(r.Context(), "audio request failed", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "audio request could not be completed", requestID)
	}
}
