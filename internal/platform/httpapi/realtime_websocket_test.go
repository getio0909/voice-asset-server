package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/coder/websocket"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/realtime"
)

type websocketControllerFixture struct {
	principal auth.Principal
	read      []byte
	readErr   error
	writeErr  error
	called    bool
}

func (fixture *websocketControllerFixture) Serve(
	ctx context.Context,
	principal auth.Principal,
	transport realtime.EventTransport,
) error {
	fixture.called = true
	fixture.principal = principal
	fixture.read, fixture.readErr = transport.Read(ctx)
	if fixture.readErr != nil {
		return fixture.readErr
	}
	fixture.writeErr = transport.Write(ctx, []byte(`{"type":"ready"}`))
	return fixture.writeErr
}

func TestWebSocketRealtimeEndpointUpgradesAndPreservesTextFrames(t *testing.T) {
	fixture := &websocketControllerFixture{}
	principal := auth.Principal{WorkspaceID: "workspace-1", UserID: "user-1"}
	endpoint := NewWebSocketRealtimeEndpoint(fixture, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint.Serve(r.Context(), principal, w, r)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	serverURL.Scheme = "ws"
	conn, _, err := websocket.Dial(context.Background(), serverURL.String(), &websocket.DialOptions{
		Subprotocols: []string{realtimeSubprotocol},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test complete")

	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"start"}`)); err != nil {
		t.Fatal(err)
	}
	typ, payload, err := conn.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText || string(payload) != `{"type":"ready"}` {
		t.Fatalf("response type/payload = %v/%s", typ, payload)
	}
	if !fixture.called || string(fixture.read) != `{"type":"start"}` ||
		fixture.principal.WorkspaceID != principal.WorkspaceID {
		t.Fatalf("controller fixture = %+v", fixture)
	}
}

func TestWebSocketRealtimeEndpointRejectsForeignOriginAndBinaryFrames(t *testing.T) {
	fixture := &websocketControllerFixture{}
	endpoint := NewWebSocketRealtimeEndpoint(fixture, "https://voice.example.test")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint.Serve(r.Context(), auth.Principal{}, w, r)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	serverURL.Scheme = "ws"
	_, _, err = websocket.Dial(context.Background(), serverURL.String(), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example.test"}},
	})
	if err == nil || fixture.called {
		t.Fatalf("foreign origin dial error/called = %v/%v", err, fixture.called)
	}

	fixture = &websocketControllerFixture{}
	endpoint = NewWebSocketRealtimeEndpoint(fixture, "")
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		endpoint.Serve(r.Context(), auth.Principal{}, w, r)
	}))
	defer server.Close()
	serverURL, err = url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	serverURL.Scheme = "ws"
	conn, _, err := websocket.Dial(context.Background(), serverURL.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("binary")); err != nil {
		t.Fatal(err)
	}
	_, _, readErr := conn.Read(context.Background())
	_ = conn.Close(websocket.StatusNormalClosure, "test complete")
	if !errors.Is(fixture.readErr, realtime.ErrInvalidEvent) || readErr == nil {
		t.Fatalf("binary read/controller errors = %v/%v", fixture.readErr, readErr)
	}
}
