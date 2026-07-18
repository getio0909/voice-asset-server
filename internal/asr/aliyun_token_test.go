package asr

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestAliyunSignatureMatchesOfficialCreateTokenVector(t *testing.T) {
	parameters := map[string]string{
		"AccessKeyId":      "my_access_key_id",
		"Action":           "CreateToken",
		"Format":           "JSON",
		"RegionId":         "cn-shanghai",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   "b924c8c3-6d03-4c5d-ad36-d984d3116788",
		"SignatureVersion": "1.0",
		"Timestamp":        "2019-04-18T08:32:31Z",
		"Version":          "2019-02-28",
	}
	canonical := aliyunCanonicalQuery(parameters)
	got := aliyunSignature("GET", "/", canonical, "my_access_key_secret")
	const want = "hHq4yNsPitlfDJ2L0nQPdugdEzM="
	if got != want {
		t.Fatalf("aliyunSignature() = %q, want official test vector %q", got, want)
	}
}

func TestAliyunAccessKeyTokenSourceSignsAndCachesToken(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	requests := 0
	client := testHTTPClient(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.Method != http.MethodGet || request.URL.Host != "nls-meta.cn-shanghai.aliyuncs.com" {
			t.Fatalf("unexpected token request: %s %s", request.Method, request.URL.Host)
		}
		query := request.URL.Query()
		for _, key := range []string{"AccessKeyId", "Action", "RegionId", "Signature", "SignatureNonce", "Timestamp"} {
			if query.Get(key) == "" {
				t.Fatalf("token request omitted %s", key)
			}
		}
		if query.Get("Action") != "CreateToken" || query.Get("RegionId") != "cn-shanghai" {
			t.Fatal("token request used an unexpected action or region")
		}
		body := []byte(`{"Token":{"Id":"fixture-issued-token","ExpireTime":1784206800}}`)
		return testResponse(http.StatusOK, body), nil
	})
	source, err := newAliyunAccessKeyTokenSource("fixture-access-id", "fixture-access-secret", client)
	if err != nil {
		t.Fatalf("newAliyunAccessKeyTokenSource() error = %v", err)
	}
	source.now = func() time.Time { return now }
	source.random = bytes.NewReader(make([]byte, 16))

	first, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("first Token() error = %v", err)
	}
	second, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("second Token() error = %v", err)
	}
	if first != "fixture-issued-token" || second != first || requests != 1 {
		t.Fatalf("cached token result mismatch: first=%q second=%q requests=%d", first, second, requests)
	}
}

func TestAliyunTokenSourceRejectsOversizedResponse(t *testing.T) {
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		body := io.NopCloser(io.LimitReader(zeroReader{}, maxProviderResponseBytes+1))
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
	})
	source, err := newAliyunAccessKeyTokenSource("fixture-access-id", "fixture-access-secret", client)
	if err != nil {
		t.Fatal(err)
	}
	source.random = bytes.NewReader(make([]byte, 16))
	if _, err := source.Token(context.Background()); ErrorClassOf(err) != ErrorTransient {
		t.Fatalf("Token() error = %v, want transient classification", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}
