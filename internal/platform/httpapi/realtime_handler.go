package httpapi

import (
	"net/http"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func (handler *Handler) handleRealtimeTranscription(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
) {
	if r.Method != http.MethodGet {
		handler.writeMethodNotAllowed(w, requestID, http.MethodGet)
		return
	}
	if handler.realtimeEndpoint == nil {
		handler.writeError(
			w, http.StatusServiceUnavailable, "service_unavailable",
			"realtime transcription is unavailable", requestID,
		)
		return
	}
	principal, ok := handler.authenticateRequest(w, r, requestID, true)
	if !ok {
		return
	}
	if !principal.Can(auth.ScopeTranscriptionsWrite) {
		handler.writeError(
			w, http.StatusForbidden, "forbidden",
			"realtime transcription is not allowed", requestID,
		)
		return
	}
	handler.realtimeEndpoint.Serve(r.Context(), principal, w, r)
}
