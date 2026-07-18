package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

func (h *Handler) handleAssetTranscription(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.jobService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "transcription service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	created, replayed, err := h.jobService.CreateTranscription(
		r.Context(), principal, assetID, r.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		h.writeJobError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/transcription-jobs/"+created.ID)
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	h.writeJSON(w, http.StatusAccepted, created)
}

func (h *Handler) handleTranscriptionJob(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	jobID := strings.TrimPrefix(r.URL.Path, "/api/v1/transcription-jobs/")
	jobID, validID := identifier.NormalizeUUID(jobID)
	if !validID || strings.Contains(jobID, "/") {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if h.jobService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "transcription service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	result, err := h.jobService.Get(r.Context(), principal, jobID)
	if err != nil {
		h.writeJobError(w, r, requestID, err)
		return
	}
	if !h.recordReadAudit(w, r, requestID, principal, "job.read", "job", result.ID, nil) {
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) writeJobError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, job.ErrInvalidInput):
		h.writeError(w, http.StatusBadRequest, "invalid_request", "transcription request is invalid", requestID)
	case errors.Is(err, job.ErrForbidden):
		h.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, job.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, job.ErrAssetNotReady):
		h.writeError(w, http.StatusConflict, "asset_not_ready", "asset is not ready for transcription", requestID)
	case errors.Is(err, job.ErrRevisionNotCorrectable):
		h.writeError(w, http.StatusConflict, "revision_not_correctable", "transcript revision is not correctable", requestID)
	case errors.Is(err, job.ErrCorrectionActive):
		h.writeError(w, http.StatusConflict, "correction_active", "a correction job is already active for this asset", requestID)
	case errors.Is(err, job.ErrIdempotencyConflict):
		h.writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used for another request", requestID)
	default:
		h.logger.ErrorContext(r.Context(), "transcription job request failed", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "transcription request could not be completed", requestID)
	}
}
