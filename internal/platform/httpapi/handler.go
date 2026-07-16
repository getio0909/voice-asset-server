// Package httpapi implements the public HTTP boundary.
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

// Handler serves the Phase 0 public endpoints.
type Handler struct {
	brandName string
	logger    *slog.Logger
	now       func() time.Time
}

const maxRequestIDLength = 200

// NewHandler constructs an HTTP handler with no external service dependency.
func NewHandler(brandName string, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Handler{brandName: brandName, logger: logger, now: time.Now}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" || len(requestID) > maxRequestIDLength {
		requestID = newRequestID()
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-API-Version", product.APIVersion)
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-Server-Version", product.ServerVersion)

	switch r.URL.Path {
	case "/healthz", "/livez", "/readyz":
		if r.Method != http.MethodGet {
			h.writeMethodNotAllowed(w, requestID, http.MethodGet)
			return
		}
		h.writeJSON(w, http.StatusOK, healthResponse{
			Status:    "ok",
			Service:   h.brandName,
			Timestamp: h.now().UTC().Format(time.RFC3339),
		})
	case "/api/v1/system/capabilities":
		if r.Method != http.MethodGet {
			h.writeMethodNotAllowed(w, requestID, http.MethodGet)
			return
		}
		h.writeJSON(w, http.StatusOK, product.CurrentCapabilities())
	default:
		h.writeError(w, http.StatusNotFound, "not_found", "resource not found", requestID)
	}

	h.logger.DebugContext(r.Context(), "request handled",
		"method", r.Method,
		"path", r.URL.Path,
		"request_id", requestID,
	)
}

type healthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

type errorResponse struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func (h *Handler) writeMethodNotAllowed(w http.ResponseWriter, requestID, allow string) {
	w.Header().Set("Allow", allow)
	h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", requestID)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code, message, requestID string) {
	h.writeJSON(w, status, errorResponse{Error: apiError{
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}})
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.Error("encode HTTP response", "error", err)
	}
}

func newRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "request-id-unavailable"
	}
	return hex.EncodeToString(value[:])
}
