package httpapi

import (
	"net/http"

	"github.com/getio0909/voice-asset-server/internal/audit"
	"github.com/getio0909/voice-asset-server/internal/auth"
)

func (handler *Handler) recordReadAudit(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	principal auth.Principal,
	action string,
	targetType string,
	targetID string,
	metadata map[string]any,
) bool {
	if handler.auditService == nil {
		return true
	}
	if err := handler.auditService.Record(r.Context(), audit.RecordInput{
		Principal: principal, Action: action, TargetType: targetType,
		TargetID: targetID, RequestID: requestID, Metadata: metadata,
	}); err != nil {
		handler.logger.ErrorContext(r.Context(), "audit write failed", "error", err, "request_id", requestID)
		handler.writeError(w, http.StatusInternalServerError, "audit_unavailable", "request audit could not be persisted", requestID)
		return false
	}
	return true
}
