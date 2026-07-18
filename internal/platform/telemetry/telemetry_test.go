package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestValidateEndpointRejectsUnsafeOrMalformedValues(t *testing.T) {
	for _, value := range []string{
		"http://telemetry.example.test",
		"https://user:secret@telemetry.example.test",
		"https://telemetry.example.test?token=secret",
		"not-a-url",
	} {
		if err := ValidateEndpoint(value); err == nil {
			t.Fatalf("ValidateEndpoint(%q) = nil, want error", value)
		}
	}
	for _, value := range []string{"", "http://127.0.0.1:4318", "https://telemetry.example.test:4318"} {
		if err := ValidateEndpoint(value); err != nil {
			t.Fatalf("ValidateEndpoint(%q) error = %v", value, err)
		}
	}
}

func TestSetupExportsOTLPHTTPTrace(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/traces" || request.Method != http.MethodPost {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.Copy(io.Discard, request.Body)
		requests.Add(1)
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	shutdown, err := Setup(context.Background(), "voiceasset-test", server.URL)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	tracer := otel.Tracer("voiceasset-test")
	ctx, span := tracer.Start(context.Background(), "test-operation")
	span.End()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}
	if requests.Load() == 0 {
		t.Fatal("OTLP receiver observed no trace request")
	}
}

func TestSetupWithoutEndpointIsNoOp(t *testing.T) {
	shutdown, err := Setup(context.Background(), "voiceasset-test", "")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown() error = %v", err)
	}
}
