package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/operations"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

func (handler *Handler) handleAdminJobs(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.operationsService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "operations service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	input, err := parseJobListInput(r.URL.Query())
	if err != nil {
		handler.writeOperationsError(w, r, requestID, err)
		return
	}
	result, err := handler.operationsService.ListJobs(r.Context(), principal, input)
	if err != nil {
		handler.writeOperationsError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(w, r, requestID, principal, "admin.job.listed", "job_collection", "", map[string]any{
		"result_count": len(result.Items), "has_next": result.NextCursor != nil,
		"state_filter": input.State != "", "kind_filter": input.Kind != "",
	}) {
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func (handler *Handler) handleAdminJobRoute(w http.ResponseWriter, r *http.Request, requestID string) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/jobs/")
	parts := strings.Split(path, "/")
	jobID, validID := identifier.NormalizeUUID(parts[0])
	if !validID || len(parts) != 2 || parts[1] != "retry" {
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
		return
	}
	if r.Method != http.MethodPost {
		handler.writeMethodNotAllowed(w, requestID, http.MethodPost)
		return
	}
	if handler.operationsService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "operations service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	retried, err := handler.operationsService.RetryJob(r.Context(), principal, jobID, requestID)
	if err != nil {
		handler.writeOperationsRetryError(w, r, requestID, err)
		return
	}
	handler.writeJSON(w, http.StatusOK, retried)
}

func (handler *Handler) handleAdminAuditLogs(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.operationsService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "operations service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	input, err := parseAuditListInput(r.URL.Query())
	if err != nil {
		handler.writeOperationsError(w, r, requestID, err)
		return
	}
	result, err := handler.operationsService.ListAuditLogs(r.Context(), principal, input)
	if err != nil {
		handler.writeOperationsError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(w, r, requestID, principal, "admin.audit_log.listed", "audit_log_collection", "", map[string]any{
		"result_count": len(result.Items), "has_next": result.NextCursor != nil,
		"actor_filter": input.ActorType != "", "action_filter": input.Action != "",
		"target_filter": input.TargetType != "",
	}) {
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func (handler *Handler) handleAdminSystemStatus(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.operationsService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "operations service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	result, err := handler.operationsService.GetSystemStatus(r.Context(), principal)
	if err != nil {
		handler.writeOperationsError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(w, r, requestID, principal, "admin.system_status.read", "system_status", "", nil) {
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func parseJobListInput(values url.Values) (operations.JobListInput, error) {
	if !singleQueryValues(values, "limit", "cursor", "state", "kind") {
		return operations.JobListInput{}, operations.ErrInvalidInput
	}
	limit, err := parseOptionalLimit(values.Get("limit"))
	if err != nil {
		return operations.JobListInput{}, err
	}
	return operations.JobListInput{
		Limit: limit, Cursor: values.Get("cursor"), State: values.Get("state"), Kind: values.Get("kind"),
	}, nil
}

func parseAuditListInput(values url.Values) (operations.AuditListInput, error) {
	if !singleQueryValues(values, "limit", "cursor", "actor_type", "action", "target_type") {
		return operations.AuditListInput{}, operations.ErrInvalidInput
	}
	limit, err := parseOptionalLimit(values.Get("limit"))
	if err != nil {
		return operations.AuditListInput{}, err
	}
	return operations.AuditListInput{
		Limit: limit, Cursor: values.Get("cursor"), ActorType: values.Get("actor_type"),
		Action: values.Get("action"), TargetType: values.Get("target_type"),
	}, nil
}

func singleQueryValues(values url.Values, allowed ...string) bool {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key, entries := range values {
		if _, ok := allowedSet[key]; !ok || len(entries) != 1 {
			return false
		}
	}
	return true
}

func parseOptionalLimit(value string) (int, error) {
	if value == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		return 0, operations.ErrInvalidInput
	}
	return limit, nil
}

func (handler *Handler) writeOperationsError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, operations.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "operations input is invalid", requestID)
	case errors.Is(err, operations.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "admin:read scope is required", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "operations request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "operations request could not be completed", requestID)
	}
}

func (handler *Handler) writeOperationsRetryError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, operations.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "job retry input is invalid", requestID)
	case errors.Is(err, operations.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "admin:write scope is required", requestID)
	case errors.Is(err, operations.ErrNotFound):
		handler.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	case errors.Is(err, operations.ErrNotRetryable):
		handler.writeError(w, http.StatusConflict, "job_not_retryable", "job is not retryable", requestID)
	case errors.Is(err, operations.ErrRetryLimit):
		handler.writeError(w, http.StatusConflict, "job_retry_limit_reached", "job retry limit has been reached", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "operations job retry failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "job retry could not be completed", requestID)
	}
}
