package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/membership"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const maxMembershipBodyBytes = 32 * 1024

func (handler *Handler) handleAdminMembers(w http.ResponseWriter, r *http.Request, requestID string) {
	if handler.membershipService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "membership service is unavailable", requestID)
		return
	}
	switch r.Method {
	case http.MethodGet:
		principal, ok := handler.authenticateRequest(w, r, requestID, false)
		if !ok {
			return
		}
		input, err := parseMembershipListInput(r.URL.Query())
		if err != nil {
			handler.writeMembershipError(w, r, requestID, err)
			return
		}
		result, err := handler.membershipService.List(r.Context(), principal, input)
		if err != nil {
			handler.writeMembershipError(w, r, requestID, err)
			return
		}
		if !handler.recordReadAudit(w, r, requestID, principal, "membership.listed", "membership_collection", "", map[string]any{
			"result_count": len(result.Items), "has_next": result.NextCursor != nil,
			"role_filter": input.Role != "", "status_filter": input.Status != "",
		}) {
			return
		}
		handler.writeJSON(w, http.StatusOK, result)
	case http.MethodPost:
		principal, ok := handler.authenticateRequest(w, r, requestID, true)
		if !ok {
			return
		}
		var input membership.CreateInput
		if err := decodeJSONBody(w, r, &input, maxMembershipBodyBytes); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
				return
			}
			handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
			return
		}
		created, err := handler.membershipService.Create(r.Context(), principal, input, requestID)
		if err != nil {
			handler.writeMembershipError(w, r, requestID, err)
			return
		}
		w.Header().Set("Location", "/api/v1/admin/members/"+created.ID)
		w.Header().Set("ETag", entityTag(created.Version))
		handler.writeJSON(w, http.StatusCreated, created)
	default:
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet, http.MethodPost)
	}
}

func (handler *Handler) handleAdminMemberRoute(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodPatch {
		handler.writeMethodNotAllowed(w, requestID, http.MethodPatch)
		return
	}
	memberID := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/members/")
	memberID, validID := identifier.NormalizeUUID(memberID)
	if !validID || strings.Contains(memberID, "/") {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if handler.membershipService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "membership service is unavailable", requestID)
		return
	}
	expectedVersion, present, valid := parseEntityVersion(r.Header.Get("If-Match"))
	if !present {
		handler.writeError(w, http.StatusPreconditionRequired, "precondition_required", "If-Match is required", requestID)
		return
	}
	if !valid {
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "If-Match must contain one resource version", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	var input membership.UpdateInput
	if err := decodeJSONBody(w, r, &input, maxMembershipBodyBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			handler.writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is too large", requestID)
			return
		}
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", requestID)
		return
	}
	updated, err := handler.membershipService.Update(
		r.Context(), principal, memberID, expectedVersion, input, requestID,
	)
	if err != nil {
		handler.writeMembershipError(w, r, requestID, err)
		return
	}
	w.Header().Set("ETag", entityTag(updated.Version))
	handler.writeJSON(w, http.StatusOK, updated)
}

func parseMembershipListInput(query url.Values) (membership.ListInput, error) {
	allowed := map[string]bool{"limit": true, "cursor": true, "role": true, "status": true}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 {
			return membership.ListInput{}, membership.ErrInvalidInput
		}
	}
	input := membership.ListInput{
		Cursor: query.Get("cursor"), Role: query.Get("role"), Status: query.Get("status"),
	}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return membership.ListInput{}, membership.ErrInvalidInput
		}
		input.Limit = limit
	}
	return input, nil
}

func (handler *Handler) writeMembershipError(w http.ResponseWriter, r *http.Request, requestID string, err error) {
	switch {
	case errors.Is(err, membership.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "membership input is invalid", requestID)
	case errors.Is(err, membership.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "membership administration is not permitted", requestID)
	case errors.Is(err, membership.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, membership.ErrConflict):
		handler.writeError(w, http.StatusConflict, "email_conflict", "email already belongs to a user", requestID)
	case errors.Is(err, membership.ErrVersionConflict):
		handler.writeError(w, http.StatusPreconditionFailed, "version_conflict", "membership version changed", requestID)
	case errors.Is(err, membership.ErrLastOwner):
		handler.writeError(w, http.StatusConflict, "last_owner_required", "workspace must retain an active owner", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "membership request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "membership request could not be completed", requestID)
	}
}
