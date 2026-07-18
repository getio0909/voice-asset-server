package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/getio0909/voice-asset-server/internal/notification"
)

func (handler *Handler) handleNotifications(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.notificationService == nil {
		handler.writeError(w, http.StatusServiceUnavailable, "service_unavailable", "notification service is unavailable", requestID)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, false)
	if !ok {
		return
	}
	input, err := parseNotificationListInput(r.URL.Query())
	if err != nil {
		handler.writeNotificationError(w, r, requestID, err)
		return
	}
	result, err := handler.notificationService.List(r.Context(), principal, input)
	if err != nil {
		handler.writeNotificationError(w, r, requestID, err)
		return
	}
	if !handler.recordReadAudit(
		w, r, requestID, principal,
		"notification.listed", "notification_collection", "",
		map[string]any{"result_count": len(result.Items), "has_more": result.HasMore},
	) {
		return
	}
	handler.writeJSON(w, http.StatusOK, result)
}

func parseNotificationListInput(values url.Values) (notification.ListInput, error) {
	if !singleQueryValues(values, "limit", "cursor") {
		return notification.ListInput{}, notification.ErrInvalidInput
	}
	limit, err := parseOptionalLimit(values.Get("limit"))
	if err != nil {
		return notification.ListInput{}, notification.ErrInvalidInput
	}
	return notification.ListInput{Limit: limit, Cursor: values.Get("cursor")}, nil
}

func (handler *Handler) writeNotificationError(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	err error,
) {
	switch {
	case errors.Is(err, notification.ErrInvalidInput):
		handler.writeError(w, http.StatusBadRequest, "invalid_request", "notification input is invalid", requestID)
	case errors.Is(err, notification.ErrForbidden):
		handler.writeError(w, http.StatusForbidden, "forbidden", "an interactive session with transcripts:read scope is required", requestID)
	default:
		handler.logger.ErrorContext(r.Context(), "notification request failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "internal_error", "notifications could not be listed", requestID)
	}
}
