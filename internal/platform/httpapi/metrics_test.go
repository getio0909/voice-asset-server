package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

func TestMetricsEndpointExportsBoundedRequestSeries(t *testing.T) {
	t.Parallel()
	handler := NewHandler("VoiceAsset", nil)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
		httptest.NewRequest(http.MethodGet, "/private/asset-id?token=never-export", nil),
		httptest.NewRequest("CUSTOM", "/api/v1/assets/private-asset-id", nil),
	} {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	body := recorder.Body.String()
	for _, expected := range []string{
		"voiceasset_build_info{api_version=\"v1\",contract_version=\"" + product.ContractVersion + "\"",
		"voiceasset_http_in_flight_requests 0",
		"voiceasset_http_requests_total{method=\"GET\",route=\"health\",status=\"200\"} 1",
		"voiceasset_http_requests_total{method=\"GET\",route=\"not_found\",status=\"404\"} 1",
		"voiceasset_http_requests_total{method=\"OTHER\",route=\"assets\",status=\"404\"} 1",
		"# TYPE voiceasset_http_request_duration_seconds histogram",
		"voiceasset_http_request_duration_seconds_bucket{method=\"GET\",route=\"health\",status=\"200\",le=\"+Inf\"} 1",
		"voiceasset_http_request_duration_seconds_count{method=\"GET\",route=\"health\",status=\"200\"} 1",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics body does not contain %q:\n%s", expected, body)
		}
	}
	for _, forbidden := range []string{"private-asset-id", "never-export"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics body contains sensitive path material %q", forbidden)
		}
	}
}

func TestMetricsDurationHistogramUsesCumulativeBoundaries(t *testing.T) {
	t.Parallel()
	metrics := newHTTPMetrics()
	metrics.begin()
	metrics.observe(http.MethodGet, "health", http.StatusOK, 25*time.Millisecond)

	recorder := httptest.NewRecorder()
	metrics.serveHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"voiceasset_http_request_duration_seconds_bucket{method=\"GET\",route=\"health\",status=\"200\",le=\"0.01\"} 0",
		"voiceasset_http_request_duration_seconds_bucket{method=\"GET\",route=\"health\",status=\"200\",le=\"0.025\"} 1",
		"voiceasset_http_request_duration_seconds_bucket{method=\"GET\",route=\"health\",status=\"200\",le=\"10\"} 1",
		"voiceasset_http_request_duration_seconds_bucket{method=\"GET\",route=\"health\",status=\"200\",le=\"+Inf\"} 1",
		"voiceasset_http_request_duration_seconds_sum{method=\"GET\",route=\"health\",status=\"200\"} 0.025",
		"voiceasset_http_request_duration_seconds_count{method=\"GET\",route=\"health\",status=\"200\"} 1",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics body does not contain %q:\n%s", expected, body)
		}
	}
}

func TestMetricsEndpointRejectsMutationMethods(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	NewHandler("VoiceAsset", nil).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, "/metrics", nil),
	)
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("status/Allow = %d/%q", recorder.Code, recorder.Header().Get("Allow"))
	}
}

func TestMetricsAreConcurrentAndDoNotCountScrapes(t *testing.T) {
	t.Parallel()
	handler := NewHandler("VoiceAsset", nil)
	const requests = 50
	var wait sync.WaitGroup
	for range requests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			handler.ServeHTTP(
				httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, "/healthz", nil),
			)
		}()
	}
	wait.Wait()

	for range 2 {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		expected := "voiceasset_http_requests_total{method=\"GET\",route=\"health\",status=\"200\"} 50"
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("metrics body does not contain %q", expected)
		}
	}
}

func TestMetricsRecordPanicsWithoutLeakingInFlight(t *testing.T) {
	t.Parallel()
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		ReadinessCheck: func(context.Context) error {
			panic("dependency panic")
		},
	})

	func() {
		defer func() {
			if recovered := recover(); recovered != "dependency panic" {
				t.Fatalf("recovered = %#v", recovered)
			}
		}()
		handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/readyz", nil),
		)
	}()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		"voiceasset_http_in_flight_requests 0",
		"voiceasset_http_requests_total{method=\"GET\",route=\"readiness\",status=\"500\"} 1",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics body does not contain %q:\n%s", expected, body)
		}
	}
}

func TestStructuredRequestLogIncludesStatusLatencyWithoutRawPath(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelInfo}))
	handler := NewHandler("VoiceAsset", logger)
	request := httptest.NewRequest(http.MethodGet, "/private/private-asset-id?token=never-log", nil)
	request.Header.Set("X-Request-ID", "test-request-id")
	handler.ServeHTTP(httptest.NewRecorder(), request)

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	for key, expected := range map[string]any{
		"msg":        "request handled",
		"method":     http.MethodGet,
		"route":      "not_found",
		"status":     float64(http.StatusNotFound),
		"request_id": "test-request-id",
	} {
		if record[key] != expected {
			t.Fatalf("log %s = %#v, want %#v", key, record[key], expected)
		}
	}
	if _, ok := record["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms = %#v, want number", record["duration_ms"])
	}
	if strings.Contains(output.String(), "private-asset-id") || strings.Contains(output.String(), "never-log") {
		t.Fatal("structured request log contains raw path or query material")
	}
}
