package httpapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

type metricKey struct {
	method string
	route  string
	status string
}

type metricSample struct {
	count           uint64
	durationSeconds float64
	durationBuckets [len(httpRequestDurationBuckets)]uint64
}

var httpRequestDurationBuckets = [...]struct {
	upperBound float64
	label      string
}{
	{upperBound: 0.005, label: "0.005"},
	{upperBound: 0.01, label: "0.01"},
	{upperBound: 0.025, label: "0.025"},
	{upperBound: 0.05, label: "0.05"},
	{upperBound: 0.1, label: "0.1"},
	{upperBound: 0.25, label: "0.25"},
	{upperBound: 0.5, label: "0.5"},
	{upperBound: 1, label: "1"},
	{upperBound: 2.5, label: "2.5"},
	{upperBound: 5, label: "5"},
	{upperBound: 10, label: "10"},
}

type httpMetrics struct {
	mu       sync.RWMutex
	samples  map[metricKey]metricSample
	inFlight atomic.Int64
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{samples: make(map[metricKey]metricSample)}
}

func (metrics *httpMetrics) begin() {
	metrics.inFlight.Add(1)
}

func (metrics *httpMetrics) observe(method, route string, status int, duration time.Duration) {
	metrics.inFlight.Add(-1)
	if duration < 0 {
		duration = 0
	}
	key := metricKey{
		method: metricMethod(method),
		route:  route,
		status: strconv.Itoa(status),
	}
	metrics.mu.Lock()
	sample := metrics.samples[key]
	sample.count++
	durationSeconds := duration.Seconds()
	sample.durationSeconds += durationSeconds
	for index, bucket := range httpRequestDurationBuckets {
		if durationSeconds <= bucket.upperBound {
			sample.durationBuckets[index]++
		}
	}
	metrics.samples[key] = sample
	metrics.mu.Unlock()
}

func (metrics *httpMetrics) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	metrics.mu.RLock()
	keys := make([]metricKey, 0, len(metrics.samples))
	snapshot := make(map[metricKey]metricSample, len(metrics.samples))
	for key, sample := range metrics.samples {
		keys = append(keys, key)
		snapshot[key] = sample
	}
	metrics.mu.RUnlock()
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].route != keys[right].route {
			return keys[left].route < keys[right].route
		}
		if keys[left].method != keys[right].method {
			return keys[left].method < keys[right].method
		}
		return keys[left].status < keys[right].status
	})

	var body bytes.Buffer
	fmt.Fprintln(&body, "# HELP voiceasset_build_info VoiceAsset build and contract identity.")
	fmt.Fprintln(&body, "# TYPE voiceasset_build_info gauge")
	fmt.Fprintf(
		&body,
		"voiceasset_build_info{api_version=\"%s\",contract_version=\"%s\",version=\"%s\"} 1\n",
		metricLabel(product.APIVersion),
		metricLabel(product.ContractVersion),
		metricLabel(product.ServerVersion),
	)
	fmt.Fprintln(&body, "# HELP voiceasset_http_in_flight_requests HTTP requests currently executing.")
	fmt.Fprintln(&body, "# TYPE voiceasset_http_in_flight_requests gauge")
	fmt.Fprintf(&body, "voiceasset_http_in_flight_requests %d\n", metrics.inFlight.Load())
	fmt.Fprintln(&body, "# HELP voiceasset_http_requests_total Completed HTTP requests by bounded route, method, and status.")
	fmt.Fprintln(&body, "# TYPE voiceasset_http_requests_total counter")
	for _, key := range keys {
		sample := snapshot[key]
		labels := metricLabels(key)
		fmt.Fprintf(&body, "voiceasset_http_requests_total%s %d\n", labels, sample.count)
	}
	fmt.Fprintln(&body, "# HELP voiceasset_http_request_duration_seconds HTTP request duration by bounded route, method, and status.")
	fmt.Fprintln(&body, "# TYPE voiceasset_http_request_duration_seconds histogram")
	for _, key := range keys {
		sample := snapshot[key]
		labels := metricLabels(key)
		for index, bucket := range httpRequestDurationBuckets {
			fmt.Fprintf(
				&body,
				"voiceasset_http_request_duration_seconds_bucket%s %d\n",
				metricBucketLabels(key, bucket.label),
				sample.durationBuckets[index],
			)
		}
		fmt.Fprintf(
			&body,
			"voiceasset_http_request_duration_seconds_bucket%s %d\n",
			metricBucketLabels(key, "+Inf"),
			sample.count,
		)
		fmt.Fprintf(
			&body,
			"voiceasset_http_request_duration_seconds_sum%s %s\n",
			labels,
			strconv.FormatFloat(sample.durationSeconds, 'g', -1, 64),
		)
		fmt.Fprintf(
			&body,
			"voiceasset_http_request_duration_seconds_count%s %d\n",
			labels,
			sample.count,
		)
	}
	_, _ = w.Write(body.Bytes())
}

func metricLabels(key metricKey) string {
	return fmt.Sprintf(
		"{method=\"%s\",route=\"%s\",status=\"%s\"}",
		metricLabel(key.method),
		metricLabel(key.route),
		metricLabel(key.status),
	)
}

func metricBucketLabels(key metricKey, upperBound string) string {
	return fmt.Sprintf(
		"{method=\"%s\",route=\"%s\",status=\"%s\",le=\"%s\"}",
		metricLabel(key.method),
		metricLabel(key.route),
		metricLabel(key.status),
		metricLabel(upperBound),
	)
}

func metricLabel(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
	return replacer.Replace(value)
}

func metricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func metricRoute(requestPath string) string {
	switch requestPath {
	case "/healthz", "/livez":
		return "health"
	case "/readyz":
		return "readiness"
	}
	const prefix = "/api/v1/"
	if !strings.HasPrefix(requestPath, prefix) {
		return "not_found"
	}
	segment := strings.SplitN(strings.TrimPrefix(requestPath, prefix), "/", 2)[0]
	switch segment {
	case "admin", "api-keys", "asr", "asset-purge-jobs", "assets", "auth", "collections",
		"glossary-sets", "hotword-sets", "jobs", "llm", "llm-profiles",
		"provider-profiles", "realtime", "sync", "system", "tags", "transcripts", "uploads":
		return segment
	default:
		return "api_other"
	}
}

type trackedResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func newTrackedResponseWriter(writer http.ResponseWriter) *trackedResponseWriter {
	return &trackedResponseWriter{ResponseWriter: writer, status: http.StatusOK}
}

func (writer *trackedResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.status = status
	writer.wroteHeader = true
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *trackedResponseWriter) Write(body []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *trackedResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := writer.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(reader)
	}
	return io.Copy(writer.ResponseWriter, reader)
}

func (writer *trackedResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}
