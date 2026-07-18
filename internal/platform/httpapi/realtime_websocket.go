package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/realtime"
)

const realtimeSubprotocol = "voiceasset.realtime.v1"

// RealtimeController is the protocol state machine behind a WebSocket
// transport. Keeping this boundary small makes the HTTP upgrade policy
// independently testable without weakening the controller's typed protocol.
type RealtimeController interface {
	Serve(context.Context, auth.Principal, realtime.EventTransport) error
}

// WebSocketRealtimeEndpoint is the production transport for the realtime
// transcription controller. Authentication and scope checks happen in the
// HTTP handler before this endpoint is called.
type WebSocketRealtimeEndpoint struct {
	controller     RealtimeController
	originPatterns []string
}

func NewWebSocketRealtimeEndpoint(controller RealtimeController, publicOrigin string) *WebSocketRealtimeEndpoint {
	endpoint := &WebSocketRealtimeEndpoint{controller: controller}
	if parsed, err := url.Parse(strings.TrimSpace(publicOrigin)); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		endpoint.originPatterns = []string{parsed.Scheme + "://" + parsed.Host}
	}
	return endpoint
}

func (endpoint *WebSocketRealtimeEndpoint) Serve(
	ctx context.Context,
	principal auth.Principal,
	w http.ResponseWriter,
	r *http.Request,
) {
	if endpoint == nil || endpoint.controller == nil || r == nil {
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:    []string{realtimeSubprotocol},
		OriginPatterns:  endpoint.originPatterns,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()
	conn.SetReadLimit(realtime.MaxClientMessageBytes)
	transport := &websocketEventTransport{connection: conn}
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := endpoint.controller.Serve(serveCtx, principal, transport); err != nil &&
		!errors.Is(err, realtime.ErrRealtimeTransport) {
		_ = conn.Close(websocket.StatusInternalError, "realtime session ended")
	}
}

type websocketEventTransport struct {
	connection *websocket.Conn
	writeMu    sync.Mutex
}

func (transport *websocketEventTransport) Read(ctx context.Context) ([]byte, error) {
	if transport == nil || transport.connection == nil {
		return nil, realtime.ErrRealtimeTransport
	}
	typ, payload, err := transport.connection.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, realtime.ErrInvalidEvent
	}
	return payload, nil
}

func (transport *websocketEventTransport) Write(ctx context.Context, payload []byte) error {
	if transport == nil || transport.connection == nil {
		return realtime.ErrRealtimeTransport
	}
	transport.writeMu.Lock()
	defer transport.writeMu.Unlock()
	return transport.connection.Write(ctx, websocket.MessageText, payload)
}

var _ RealtimeEndpoint = (*WebSocketRealtimeEndpoint)(nil)
var _ realtime.EventTransport = (*websocketEventTransport)(nil)
