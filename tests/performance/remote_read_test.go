package performance_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	remoteRequestCount = 400
	remoteConcurrency  = 8
	remoteWarmupCount  = 16
	remoteP95Budget    = 500 * time.Millisecond
	remoteMinimumRPS   = 20.0
	maximumBodyBytes   = 64 << 10
)

type requestSample struct {
	duration time.Duration
	err      error
}

func TestRemoteReadControlPlane(t *testing.T) {
	if os.Getenv("VOICEASSET_REMOTE_PERF") != "1" {
		t.Skip("set VOICEASSET_REMOTE_PERF=1 for the isolated deployment performance smoke test")
	}

	baseURL := requireRemoteBaseURL(t, "VOICEASSET_REMOTE_BASE_URL")
	expectedContract := requiredEnvironment(t, "VOICEASSET_REMOTE_CONTRACT_VERSION")
	client := newStrictRemoteClient(t, strings.TrimSpace(os.Getenv("VOICEASSET_REMOTE_CA_FILE")))
	paths := []string{"/readyz", "/api/v1/system/capabilities"}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	verifyRemotePayloads(t, ctx, client, baseURL, expectedContract)
	for index := 0; index < remoteWarmupCount; index++ {
		sample := performRead(ctx, client, remoteEndpoint(baseURL, paths[index%len(paths)]))
		if sample.err != nil {
			t.Fatalf("warm-up request %d: %v", index+1, sample.err)
		}
	}

	started := time.Now()
	samples := runConcurrentReads(ctx, client, baseURL, paths, remoteRequestCount, remoteConcurrency)
	elapsed := time.Since(started)
	durations := make([]time.Duration, 0, len(samples))
	var firstFailure error
	failureCount := 0
	for _, sample := range samples {
		if sample.err != nil {
			failureCount++
			if firstFailure == nil {
				firstFailure = sample.err
			}
			continue
		}
		durations = append(durations, sample.duration)
	}
	if failureCount != 0 {
		t.Fatalf("remote reads failed: count=%d first=%v", failureCount, firstFailure)
	}
	if len(durations) != remoteRequestCount {
		t.Fatalf("successful sample count = %d, want %d", len(durations), remoteRequestCount)
	}

	sort.Slice(durations, func(left, right int) bool { return durations[left] < durations[right] })
	p50 := durationPercentile(durations, 0.50)
	p95 := durationPercentile(durations, 0.95)
	p99 := durationPercentile(durations, 0.99)
	throughput := float64(len(durations)) / elapsed.Seconds()
	t.Logf(
		"requests=%d concurrency=%d elapsed=%s throughput=%.1f req/s p50=%s p95=%s p99=%s max=%s",
		len(durations), remoteConcurrency, elapsed.Round(time.Millisecond), throughput,
		p50.Round(time.Microsecond), p95.Round(time.Microsecond), p99.Round(time.Microsecond),
		durations[len(durations)-1].Round(time.Microsecond),
	)
	if p95 > remoteP95Budget {
		t.Fatalf("p95 latency = %s, budget = %s", p95, remoteP95Budget)
	}
	if throughput < remoteMinimumRPS {
		t.Fatalf("throughput = %.1f req/s, minimum = %.1f req/s", throughput, remoteMinimumRPS)
	}
}

func TestDurationPercentile(t *testing.T) {
	durations := []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond, 4 * time.Millisecond}
	for _, test := range []struct {
		name       string
		percentile float64
		want       time.Duration
	}{
		{name: "minimum", percentile: 0, want: time.Millisecond},
		{name: "median nearest rank", percentile: 0.50, want: 2 * time.Millisecond},
		{name: "upper nearest rank", percentile: 0.95, want: 4 * time.Millisecond},
		{name: "maximum", percentile: 1, want: 4 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := durationPercentile(durations, test.percentile); got != test.want {
				t.Fatalf("durationPercentile(%v) = %s, want %s", test.percentile, got, test.want)
			}
		})
	}
}

func runConcurrentReads(
	ctx context.Context,
	client *http.Client,
	baseURL *url.URL,
	paths []string,
	requestCount,
	concurrency int,
) []requestSample {
	jobs := make(chan int)
	results := make(chan requestSample, requestCount)
	var workers sync.WaitGroup
	workers.Add(concurrency)
	for range concurrency {
		go func() {
			defer workers.Done()
			for index := range jobs {
				results <- performRead(ctx, client, remoteEndpoint(baseURL, paths[index%len(paths)]))
			}
		}()
	}
	go func() {
		for index := 0; index < requestCount; index++ {
			jobs <- index
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()

	samples := make([]requestSample, 0, requestCount)
	for sample := range results {
		samples = append(samples, sample)
	}
	return samples
}

func performRead(ctx context.Context, client *http.Client, endpoint string) requestSample {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return requestSample{err: fmt.Errorf("create request: %w", err)}
	}
	request.Header.Set("Accept", "application/json")
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return requestSample{duration: time.Since(started), err: fmt.Errorf("GET %s: %w", request.URL.Path, err)}
	}
	defer response.Body.Close()
	written, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maximumBodyBytes+1))
	duration := time.Since(started)
	if readErr != nil {
		return requestSample{duration: duration, err: fmt.Errorf("read %s: %w", request.URL.Path, readErr)}
	}
	if written > maximumBodyBytes {
		return requestSample{duration: duration, err: fmt.Errorf("GET %s exceeded %d response bytes", request.URL.Path, maximumBodyBytes)}
	}
	if response.StatusCode != http.StatusOK {
		return requestSample{duration: duration, err: fmt.Errorf("GET %s returned %s", request.URL.Path, response.Status)}
	}
	return requestSample{duration: duration}
}

func verifyRemotePayloads(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	baseURL *url.URL,
	expectedContract string,
) {
	t.Helper()
	var readiness struct {
		Status string `json:"status"`
	}
	readRemoteJSON(t, ctx, client, remoteEndpoint(baseURL, "/readyz"), &readiness)
	if readiness.Status != "ok" {
		t.Fatalf("readiness status = %q, want ok", readiness.Status)
	}
	var capabilities struct {
		APIVersion      string   `json:"api_version"`
		ContractVersion string   `json:"contract_version"`
		Features        []string `json:"features"`
	}
	readRemoteJSON(t, ctx, client, remoteEndpoint(baseURL, "/api/v1/system/capabilities"), &capabilities)
	if capabilities.APIVersion != "v1" || capabilities.ContractVersion != expectedContract {
		t.Fatalf(
			"remote API/contract = %s/%s, want v1/%s",
			capabilities.APIVersion,
			capabilities.ContractVersion,
			expectedContract,
		)
	}
	features := make(map[string]bool, len(capabilities.Features))
	for _, feature := range capabilities.Features {
		features[feature] = true
	}
	for _, required := range []string{"capability_negotiation", "device_sessions", "full_text_search", "refresh_sessions"} {
		if !features[required] {
			t.Fatalf("remote capabilities are missing %q", required)
		}
	}
}

func readRemoteJSON(t *testing.T, ctx context.Context, client *http.Client, endpoint string, target any) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("create preflight request: %v", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("preflight GET %s: %v", request.URL.Path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("preflight GET %s returned %s", request.URL.Path, response.Status)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maximumBodyBytes))
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode preflight %s: %v", request.URL.Path, err)
	}
}

func newStrictRemoteClient(t *testing.T, caFile string) *http.Client {
	t.Helper()
	var roots *x509.CertPool
	if caFile != "" {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil {
			t.Fatalf("load system CA pool: %v", err)
		}
		if roots == nil {
			roots = x509.NewCertPool()
		}
		certificate, err := os.ReadFile(caFile)
		if err != nil {
			t.Fatalf("read remote CA: %v", err)
		}
		if !roots.AppendCertsFromPEM(certificate) {
			t.Fatal("remote CA file contains no certificate")
		}
	}
	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		MaxConnsPerHost:       16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{
		Transport: transport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func requireRemoteBaseURL(t *testing.T, name string) *url.URL {
	t.Helper()
	value := requiredEnvironment(t, name)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("%s must be an HTTPS origin without credentials, path, query, or fragment", name)
	}
	return parsed
}

func requiredEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func remoteEndpoint(baseURL *url.URL, path string) string {
	endpoint := *baseURL
	endpoint.Path = path
	endpoint.RawPath, endpoint.RawQuery, endpoint.Fragment = "", "", ""
	return endpoint.String()
}

func durationPercentile(sorted []time.Duration, percentile float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	index = max(0, min(index, len(sorted)-1))
	return sorted[index]
}
