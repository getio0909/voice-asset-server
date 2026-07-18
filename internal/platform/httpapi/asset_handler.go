package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const maxAssetBodyBytes = 64 * 1024

type updateAssetMetadataRequest struct {
	Title        string          `json:"title"`
	Language     string          `json:"language"`
	CollectionID json.RawMessage `json:"collection_id"`
}

func (h *Handler) handleAssetMetadata(w http.ResponseWriter, r *http.Request, requestID, assetID string) {
	if r.Method != http.MethodPut {
		h.writeMethodNotAllowed(w, requestID, http.MethodPut)
		return
	}
	if h.assetService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "asset service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	expectedVersion, present, valid := parseEntityVersion(r.Header.Get("If-Match"))
	if !present {
		h.writeError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match is required", requestID)
		return
	}
	if !valid {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "If-Match must contain one resource version", requestID)
		return
	}
	var request updateAssetMetadataRequest
	if err := decodeJSONBody(w, r, &request, maxAssetBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			h.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		h.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	if request.CollectionID == nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "collection_id must be present and may be null", requestID)
		return
	}
	var collectionID *string
	if raw := bytes.TrimSpace(request.CollectionID); !bytes.Equal(raw, []byte("null")) {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid_request", "collection_id must be a UUID or null", requestID)
			return
		}
		collectionID = &value
	}
	updated, err := h.assetService.UpdateMetadata(r.Context(), principal, assetID, expectedVersion, asset.UpdateMetadataInput{
		Title: request.Title, Language: request.Language, CollectionID: collectionID,
	}, requestID)
	if err != nil {
		h.writeAssetError(w, r, requestID, err)
		return
	}
	w.Header().Set("ETag", entityTag(updated.Version))
	h.writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleAssets(w http.ResponseWriter, r *http.Request, requestID string) {
	switch r.Method {
	case http.MethodGet:
		h.handleListAssets(w, r, requestID)
	case http.MethodPost:
		h.handleCreateAsset(w, r, requestID)
	default:
		h.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
	}
}

func (h *Handler) handleListAssets(w http.ResponseWriter, r *http.Request, requestID string) {
	if h.assetService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "asset service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	values := r.URL.Query()
	for _, key := range []string{
		"q", "collection_id", "tag_id", "status", "provider_id", "speaker",
		"created_from", "created_before", "limit", "cursor",
	} {
		if len(values[key]) > 1 {
			h.writeAssetError(w, r, requestID, asset.ErrInvalidInput)
			return
		}
	}
	limit := 0
	if value := values.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			h.writeAssetError(w, r, requestID, asset.ErrInvalidInput)
			return
		}
		limit = parsed
	}
	result, err := h.assetService.List(r.Context(), principal, asset.ListInput{
		Query: values.Get("q"), CollectionID: values.Get("collection_id"),
		TagID: values.Get("tag_id"), Status: values.Get("status"),
		ProviderID: values.Get("provider_id"), Speaker: values.Get("speaker"),
		CreatedFrom: values.Get("created_from"), CreatedBefore: values.Get("created_before"),
		Limit: limit, Cursor: values.Get("cursor"),
	})
	if err != nil {
		h.writeAssetError(w, r, requestID, err)
		return
	}
	if !h.recordReadAudit(w, r, requestID, principal, "asset.listed", "asset_collection", "", map[string]any{
		"search": values.Get("q") != "", "collection_filter": values.Get("collection_id") != "",
		"tag_filter": values.Get("tag_id") != "", "status": values.Get("status"),
		"provider_filter": values.Get("provider_id") != "", "speaker_filter": values.Get("speaker") != "",
		"created_from_filter":   values.Get("created_from") != "",
		"created_before_filter": values.Get("created_before") != "",
		"result_count":          len(result.Items), "has_next": result.NextCursor != nil,
	}) {
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleCreateAsset(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.assetService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "asset service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	var input asset.CreateInput
	if err := decodeJSONBody(w, r, &input, maxAssetBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			h.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		h.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	created, replayed, err := h.assetService.Create(r.Context(), principal, input, r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.writeAssetError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/assets/"+created.ID)
	w.Header().Set("ETag", entityTag(created.Version))
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	h.writeJSON(w, status, created)
}

func (h *Handler) handleAsset(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		h.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodDelete)
		return
	}
	assetID := strings.TrimPrefix(r.URL.Path, "/api/v1/assets/")
	assetID, validID := identifier.NormalizeUUID(assetID)
	if !validID || strings.Contains(assetID, "/") {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if h.assetService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "asset service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, r.Method == http.MethodDelete)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		h.handleAssetLifecycle(w, r, requestID, assetID, principal, true)
		return
	}
	result, err := h.assetService.Get(r.Context(), principal, assetID)
	if err != nil {
		h.writeAssetError(w, r, requestID, err)
		return
	}
	if !h.recordReadAudit(w, r, requestID, principal, "asset.read", "asset", result.ID, nil) {
		return
	}
	w.Header().Set("ETag", entityTag(result.Version))
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleRestoreAsset(w http.ResponseWriter, r *http.Request, requestID, assetID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.assetService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "asset service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	h.handleAssetLifecycle(w, r, requestID, assetID, principal, false)
}

func (h *Handler) handlePurgeAsset(w http.ResponseWriter, r *http.Request, requestID, assetID string) {
	if r.Method != http.MethodPost {
		h.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if h.assetService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "asset service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	expectedVersion, present, valid := parseEntityVersion(r.Header.Get("If-Match"))
	if !present {
		h.writeError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match is required", requestID)
		return
	}
	if !valid {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "If-Match must contain one resource version", requestID)
		return
	}
	var input asset.PurgeInput
	if err := decodeJSONBody(w, r, &input, maxAssetBodyBytes); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			h.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		h.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	requested, replayed, err := h.assetService.RequestPurge(
		r.Context(), principal, assetID, expectedVersion, input,
		r.Header.Get("Idempotency-Key"), requestID,
	)
	if err != nil {
		h.writeAssetError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/asset-purge-jobs/"+requested.JobID)
	status := http.StatusAccepted
	if replayed {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	h.writeJSON(w, status, requested)
}

func (h *Handler) handleAssetPurgeJob(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	jobID := strings.TrimPrefix(r.URL.Path, "/api/v1/asset-purge-jobs/")
	jobID, validID := identifier.NormalizeUUID(jobID)
	if !validID || strings.Contains(jobID, "/") {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if h.assetService == nil {
		h.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "asset service is unavailable", requestID)
		return
	}
	principal, ok := h.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	result, err := h.assetService.GetPurge(r.Context(), principal, jobID)
	if err != nil {
		h.writeAssetError(w, r, requestID, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleAssetLifecycle(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
	principal auth.Principal,
	trash bool,
) {
	expectedVersion, present, valid := parseEntityVersion(r.Header.Get("If-Match"))
	if !present {
		h.writeError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match is required", requestID)
		return
	}
	if !valid {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "If-Match must contain one resource version", requestID)
		return
	}
	var (
		updated asset.Asset
		err     error
	)
	if trash {
		updated, err = h.assetService.Trash(r.Context(), principal, assetID, expectedVersion, requestID)
	} else {
		updated, err = h.assetService.Restore(r.Context(), principal, assetID, expectedVersion, requestID)
	}
	if err != nil {
		h.writeAssetError(w, r, requestID, err)
		return
	}
	w.Header().Set("ETag", entityTag(updated.Version))
	h.writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) handleAssetRoute(w http.ResponseWriter, r *http.Request, requestID string) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/assets/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	canonicalID, validID := identifier.NormalizeUUID(parts[0])
	if !validID {
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	parts[0] = canonicalID
	if len(parts) == 2 && parts[0] != "" && parts[1] == "transcripts" {
		h.handleAssetTranscripts(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "audio" {
		h.handleAssetAudio(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "waveform" {
		h.handleAssetWaveform(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "clips" {
		h.handleCreateAudioClip(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "transcriptions" {
		h.handleAssetTranscription(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "metadata" {
		h.handleAssetMetadata(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "restore" {
		h.handleRestoreAsset(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "purge" {
		h.handlePurgeAsset(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "annotations" {
		h.handleAssetAnnotations(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "tags" {
		h.handleAssetTags(w, r, requestID, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "processing-status" {
		h.handleAssetProcessingStatus(w, r, requestID, parts[0])
		return
	}
	h.handleAsset(w, r, requestID)
}

func (h *Handler) writeAssetError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, asset.ErrInvalidInput):
		h.writeError(w, http.StatusBadRequest, "invalid_request", "asset input is invalid", requestID)
	case errors.Is(err, asset.ErrForbidden):
		h.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, asset.ErrNotFound):
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, asset.ErrConflict):
		h.writeError(w, http.StatusConflict, "conflict", "asset version no longer matches", requestID)
	case errors.Is(err, asset.ErrPurgeNotEligible):
		h.writeError(w, http.StatusConflict, "purge_not_eligible", "only a trashed asset with no active jobs can be permanently deleted", requestID)
	case errors.Is(err, asset.ErrIdempotencyConflict):
		h.writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used for another request", requestID)
	default:
		h.logger.ErrorContext(r.Context(), "asset request failed", "error", err, "request_id", requestID)
		h.writeError(w, http.StatusInternalServerError, "internal_error", "asset request could not be completed", requestID)
	}
}

func entityTag(version int64) string {
	return fmt.Sprintf("%q", strconv.FormatInt(version, 10))
}
