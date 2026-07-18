package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/review"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

type transcriptListResponse struct {
	Items []transcript.Summary `json:"items"`
}

func (h *Handler) handleAssetTranscripts(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if h.transcriptService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "transcript service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	results, err := h.transcriptService.List(r.Context(), principal, assetID)
	if err != nil {
		h.writeTranscriptError(w, r, requestID, err)
		return
	}
	if results == nil {
		results = make([]transcript.Summary, 0)
	}
	if !h.recordReadAudit(w, r, requestID, principal, "transcript.listed", "asset", assetID, map[string]any{
		"result_count": len(results),
	}) {
		return
	}
	h.writeJSON(w, http.StatusOK, transcriptListResponse{Items: results})
}

func (h *Handler) handleTranscriptRevision(w http.ResponseWriter, r *http.Request, requestID string) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/transcript-revisions/")
	parts := strings.Split(path, "/")
	revisionID, validID := identifier.NormalizeUUID(parts[0])
	validAction := len(parts) == 2 && (parts[1] == "corrections" || parts[1] == "reviews" || parts[1] == "approve" || parts[1] == "exports")
	if !validID || (len(parts) != 1 && !validAction) {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "corrections":
			h.handleCorrectionRequest(w, r, requestID, revisionID)
		case "reviews":
			h.handleReviewDecision(w, r, requestID, revisionID)
		case "approve":
			h.handleApproval(w, r, requestID, revisionID)
		case "exports":
			h.handleCreateTranscriptExport(w, r, requestID, revisionID)
		}
		return
	}
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if h.transcriptService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "transcript service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	result, err := h.transcriptService.GetRevision(r.Context(), principal, revisionID)
	if err != nil {
		h.writeTranscriptError(w, r, requestID, err)
		return
	}
	if !h.recordReadAudit(w, r, requestID, principal, "transcript_revision.read", "transcript_revision", result.ID, map[string]any{
		"asset_id": result.AssetID, "segment_count": len(result.Segments),
	}) {
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleReviewDecision(w http.ResponseWriter, r *http.Request, requestID, revisionID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.reviewService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "review service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	var input review.DecisionInput
	if err := decodeJSONBody(w, r, &input, 8*1024); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	record, err := h.reviewService.AddDecision(r.Context(), principal, revisionID, input)
	if err != nil {
		h.writeReviewError(w, r, requestID, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, record)
}

func (h *Handler) handleApproval(w http.ResponseWriter, r *http.Request, requestID, revisionID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.reviewService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "review service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	var input review.ApprovalInput
	if r.ContentLength != 0 {
		if err := decodeJSONBody(w, r, &input, 8*1024); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
			return
		}
	}
	result, err := h.reviewService.Approve(r.Context(), principal, revisionID, input)
	if err != nil {
		h.writeReviewError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/transcript-revisions/"+result.ApprovedRevision.ID)
	h.writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) handleCorrectionRequest(w http.ResponseWriter, r *http.Request, requestID, revisionID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.correctionService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "correction service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	created, replayed, err := h.correctionService.CreateCorrection(
		r.Context(), principal, revisionID, r.Header.Get("Idempotency-Key"),
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

func (h *Handler) writeTranscriptError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, transcript.ErrForbidden):
		h.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, transcript.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	default:
		h.logger.ErrorContext(r.Context(), "transcript request failed", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "transcript request could not be completed", requestID)
	}
}

func (h *Handler) writeReviewError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, review.ErrInvalidInput):
		h.writeError(w, http.StatusBadRequest, "invalid_request", "review input is invalid", requestID)
	case errors.Is(err, review.ErrForbidden):
		h.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, review.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, review.ErrConflict):
		h.writeError(w, http.StatusConflict, "review_conflict", "revision cannot be reviewed or was already approved", requestID)
	default:
		h.logger.ErrorContext(r.Context(), "review request failed", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "review request could not be completed", requestID)
	}
}
