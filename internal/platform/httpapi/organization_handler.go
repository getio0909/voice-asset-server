package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/organization"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const maxOrganizationBodyBytes = 32 * 1024

func (handler *Handler) handleCollection(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	collectionID := strings.TrimPrefix(r.URL.Path, "/api/v1/collections/")
	collectionID, validID := identifier.NormalizeUUID(collectionID)
	if !validID || strings.Contains(collectionID, "/") {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if handler.organizationService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "organization service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	result, err := handler.organizationService.GetCollection(r.Context(), principal, collectionID)
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(w, r, requestID, principal, "collection.read", "collection", collectionID, nil) {
		return
	}
	w.Header().Set("ETag", entityTag(result.Version))
	handler.writeJSON(w, http.StatusOK, result)
}

func (handler *Handler) handleCollections(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.organizationService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "organization service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	input, err := parseOrganizationListInput(r.URL.Query())
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	result, err := handler.organizationService.ListCollections(r.Context(), principal, input)
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(w, r, requestID, principal, "collection.listed", "collection", "", map[string]any{
		"result_count": len(result.Items), "has_next": result.NextCursor != nil,
	}) {
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func (handler *Handler) handleTags(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.organizationService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "organization service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	input, err := parseOrganizationListInput(r.URL.Query())
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	result, err := handler.organizationService.ListTags(r.Context(), principal, input)
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(w, r, requestID, principal, "tag.listed", "tag", "", map[string]any{
		"result_count": len(result.Items), "has_next": result.NextCursor != nil,
	}) {
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func (handler *Handler) handleAssetAnnotations(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
) {
	switch r.Method {
	case http.MethodGet:
		handler.handleListAssetAnnotations(w, r, requestID, assetID)
	case http.MethodPost:
		handler.handleCreateAssetAnnotation(w, r, requestID, assetID)
	default:
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
	}
}

func (handler *Handler) handleListAssetAnnotations(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
) {
	if handler.organizationService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "organization service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	listInput, err := parseOrganizationListInput(r.URL.Query())
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	result, err := handler.organizationService.ListAnnotations(r.Context(), principal, organization.AnnotationListInput{
		AssetID: assetID, Limit: listInput.Limit, Cursor: listInput.Cursor,
	})
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(w, r, requestID, principal, "annotation.listed", "asset", assetID, map[string]any{
		"result_count": len(result.Items), "has_next": result.NextCursor != nil,
	}) {
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func (handler *Handler) handleCreateAssetAnnotation(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
) {
	if handler.organizationService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "organization service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	var input organization.AnnotationCreateInput
	if !handler.decodeOrganizationBody(w, r, requestID, &input) {
		return
	}
	created, err := handler.organizationService.CreateAnnotation(r.Context(), principal, assetID, input, requestID)
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	w.Header().Set("Location", "/api/v1/assets/"+assetID+"/annotations/"+created.ID)
	w.Header().Set("ETag", entityTag(created.Version))
	handler.writeJSON(w, http.StatusCreated, created)
}

func (handler *Handler) handleAssetTags(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost && r.Method != http.MethodDelete {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost, http.MethodDelete)
		return
	}
	if handler.organizationService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "organization service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, r.Method != http.MethodGet)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		input, err := parseOrganizationListInput(r.URL.Query())
		if err != nil {
			handler.writeOrganizationError(w, r, requestID, err)
			return
		}
		result, err := handler.organizationService.ListAssetTags(r.Context(), principal, organization.AssetTagListInput{
			AssetID: assetID, Limit: input.Limit, Cursor: input.Cursor,
		})
		if err != nil {
			handler.writeOrganizationError(w, r, requestID, err)
			return
		}
		if !handler.recordReadAudit(w, r, requestID, principal, "asset.tags.listed", "asset", assetID, map[string]any{
			"result_count": len(result.Items), "has_next": result.NextCursor != nil,
		}) {
			return
		}
		handler.writeJSON(w, http.StatusOK, result)
		return
	}
	var input organization.TagMutationInput
	if !handler.decodeOrganizationBody(w, r, requestID, &input) {
		return
	}
	var (
		result organization.TagMutationResult
		err    error
	)
	if r.Method == http.MethodPost {
		result, err = handler.organizationService.AddTags(r.Context(), principal, assetID, input, requestID)
	} else {
		result, err = handler.organizationService.RemoveTags(r.Context(), principal, assetID, input, requestID)
	}
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func (handler *Handler) decodeOrganizationBody(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	destination any,
) bool {
	if err := decodeJSONBody(w, r, destination, maxOrganizationBodyBytes); err != nil {
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

func (handler *Handler) handleAssetProcessingStatus(
	w http.ResponseWriter,
	r *http.Request,
	requestID,
	assetID string,
) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.organizationService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "organization service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	result, err := handler.organizationService.GetProcessingStatus(r.Context(), principal, assetID)
	if err != nil {
		handler.writeOrganizationError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(w, r, requestID, principal, "asset.processing_status.read", "asset", assetID, map[string]any{
		"active": result.Active, "job_count": len(result.Jobs),
	}) {
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func parseOrganizationListInput(values url.Values) (organization.ListInput, error) {
	if len(values["limit"]) > 1 || len(values["cursor"]) > 1 {
		return organization.ListInput{}, organization.ErrInvalidInput
	}
	limit := 0
	if value := values.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return organization.ListInput{}, organization.ErrInvalidInput
		}
		limit = parsed
	}
	return organization.ListInput{Limit: limit, Cursor: values.Get("cursor")}, nil
}

func (handler *Handler) writeOrganizationError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, organization.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "organization input is invalid", requestID)
	case errors.Is(err, organization.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "required scope is missing", requestID)
	case errors.Is(err, organization.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "organization request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "organization request could not be completed", requestID)
	}
}
