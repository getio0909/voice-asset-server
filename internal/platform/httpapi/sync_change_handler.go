package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/getio0909/voice-asset-server/internal/syncchange"
)

func (handler *Handler) handleSyncChanges(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.syncChangeService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "sync service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	input, err := parseSyncChangeListInput(r.URL.Query())
	if err != nil {
		handler.writeSyncChangeError(w, r, requestID, err)
		return
	}
	result, err := handler.syncChangeService.List(r.Context(), principal, input)
	if err != nil {
		handler.writeSyncChangeError(w, r, requestID, err)
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func parseSyncChangeListInput(values url.Values) (syncchange.ListInput, error) {
	if !singleQueryValues(values, "limit", "cursor") {
		return syncchange.ListInput{}, syncchange.ErrInvalidInput
	}
	limit, err := parseOptionalLimit(values.Get("limit"))
	if err != nil {
		return syncchange.ListInput{}, syncchange.ErrInvalidInput
	}
	return syncchange.ListInput{Limit: limit, Cursor: values.Get("cursor")}, nil
}

func (handler *Handler) writeSyncChangeError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, syncchange.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "sync input is invalid", requestID)
	case errors.Is(err, syncchange.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "assets:read scope is required", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "sync request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "sync request could not be completed", requestID)
	}
}
