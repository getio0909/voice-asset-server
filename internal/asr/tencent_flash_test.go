package asr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTencentFlashSignatureMatchesKnownVector(t *testing.T) {
	const rawQuery = "engine_type=16k_zh&secretid=test-secret-id&timestamp=1700000000&voice_format=m4a"
	got := tencentFlashSignature(
		"asr.cloud.tencent.com", "/asr/flash/v1/1234567890", rawQuery, "test-secret-key-123",
	)
	const want = "quSiuOo6p+t4dIejZ1VUP2s+rJc="
	if got != want {
		t.Fatalf("tencentFlashSignature() = %q, want %q", got, want)
	}
}

func TestTencentFlashProviderMapsFixtureAndPreservesRawResponse(t *testing.T) {
	fixture, err := os.ReadFile("testdata/tencent_flash_success.json")
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	audioBytes := []byte("fixture-m4a-audio")
	client := testHTTPClient(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Host != tencentFlashHost ||
			request.URL.Path != "/asr/flash/v1/1234567890" {
			t.Fatalf("unexpected Tencent request: %s %s%s", request.Method, request.URL.Host, request.URL.Path)
		}
		if request.URL.RawQuery != request.URL.Query().Encode() {
			t.Fatal("Tencent signature query is not deterministically sorted")
		}
		query := request.URL.Query()
		if query.Get("secretid") != "fixture-secret-id" || query.Get("engine_type") != "16k_zh" ||
			query.Get("timestamp") != fmt.Sprint(fixedNow.Unix()) || query.Get("voice_format") != "m4a" ||
			query.Get("hotword_list") != "语音资产|10,欢迎|1" {
			t.Fatal("Tencent request query did not match the validated profile and hotwords")
		}
		wantSignature := tencentFlashSignature(
			tencentFlashHost, request.URL.Path, request.URL.RawQuery, "fixture-secret-key",
		)
		if request.Header.Get("Authorization") != wantSignature {
			t.Fatal("Tencent request signature did not match the canonical query")
		}
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(body, audioBytes) || request.ContentLength != int64(len(audioBytes)) {
			t.Fatal("Tencent request did not stream the original audio bytes")
		}
		return testResponse(http.StatusOK, fixture), nil
	})
	profile := DefaultTencentFlashProfile("1234567890")
	provider, err := NewTencentFlashProvider(
		profile,
		TencentCredentials{SecretID: "fixture-secret-id", SecretKey: "fixture-secret-key"},
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return fixedNow }

	result, err := provider.Transcribe(context.Background(), Input{
		AssetID: "asset-fixture", Language: "zh-CN", DurationMS: 2380,
		Audio:    testAudio(audioBytes, "m4a", 16_000),
		Hotwords: []Hotword{{Term: "语音资产", Weight: 100}, {Term: "欢迎", Weight: 1}},
	})
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if result.RawSchema != RawSchemaTencentFlashV1 || !bytes.Equal(result.RawJSON, fixture) {
		t.Fatal("Tencent raw response was not preserved byte-for-byte")
	}
	if result.Text != "语音资产欢迎您。" || len(result.Segments) != 1 ||
		result.Segments[0].Speaker != "channel-0" || len(result.Segments[0].Words) != 2 {
		t.Fatalf("unexpected normalized Tencent result: %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Result.Validate() error = %v", err)
	}
}

func TestTencentFlashProviderExpandsSentenceBoundsToVendorWordTimeline(t *testing.T) {
	const body = `{
		"request_id":"fixture","code":0,"message":"","audio_duration":300,
		"flash_result":[{
			"text":"recognized","channel_id":0,
			"sentence_list":[{
				"text":"recognized","start_time":100,"end_time":200,"speaker_id":0,
				"word_list":[{"word":"recognized","start_time":50,"end_time":250}]
			}]
		}]
	}`
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		return testResponse(http.StatusOK, []byte(body)), nil
	})
	provider, err := NewTencentFlashProvider(
		DefaultTencentFlashProfile("1234567890"),
		TencentCredentials{SecretID: "fixture-secret-id", SecretKey: "fixture-secret-key"},
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Transcribe(context.Background(), Input{
		AssetID: "asset-fixture", DurationMS: 300,
		Audio: testAudio([]byte("audio"), "m4a", 16_000),
	})
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if len(result.Segments) != 1 || result.Segments[0].StartMS != 50 || result.Segments[0].EndMS != 250 ||
		len(result.Segments[0].Words) != 1 || result.Segments[0].Words[0].StartMS != 50 ||
		result.Segments[0].Words[0].EndMS != 250 {
		t.Fatalf("unexpected normalized timeline: %+v", result.Segments)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Result.Validate() error = %v", err)
	}
}

func TestTencentFlashProviderClassifiesFailuresWithoutLeakingVendorMessage(t *testing.T) {
	const vendorMessage = "signature rejected for fixture-secret-key"
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		body := []byte(`{"request_id":"fixture","code":4002,"message":"` + vendorMessage + `"}`)
		return testResponse(http.StatusOK, body), nil
	})
	profile := DefaultTencentFlashProfile("1234567890")
	provider, err := NewTencentFlashProvider(
		profile,
		TencentCredentials{SecretID: "fixture-secret-id", SecretKey: "fixture-secret-key"},
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Transcribe(context.Background(), Input{
		AssetID: "asset-fixture", DurationMS: 1,
		Audio: testAudio([]byte("audio"), "m4a", 16_000),
	})
	if ErrorClassOf(err) != ErrorAuthentication || IsRetryable(err) {
		t.Fatalf("Transcribe() error = %v, want non-retryable authentication", err)
	}
	if strings.Contains(err.Error(), vendorMessage) || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal("Tencent error exposed a vendor message or credential")
	}
}

func TestTencentFlashProviderClassifiesInvalidSuccessEnvelope(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
	}{
		{
			name: "empty result",
			body: `{"request_id":"fixture","code":0,"message":"","flash_result":[]}`,
			code: "empty_result",
		},
		{
			name: "missing sentences",
			body: `{"request_id":"fixture","code":0,"message":"","flash_result":[{"text":"recognized","channel_id":0,"sentence_list":[]}]}`,
			code: "missing_sentences",
		},
		{
			name: "empty sentence",
			body: `{"request_id":"fixture","code":0,"message":"","flash_result":[{"text":"recognized","channel_id":0,"sentence_list":[{"text":"","start_time":0,"end_time":100,"word_list":[]}]}]}`,
			code: "empty_sentence",
		},
		{
			name: "invalid sentence timeline",
			body: `{"request_id":"fixture","code":0,"message":"","flash_result":[{"text":"recognized","channel_id":0,"sentence_list":[{"text":"recognized","start_time":100,"end_time":50,"word_list":[]}]}]}`,
			code: "invalid_timeline",
		},
		{
			name: "reversed word timeline",
			body: `{"request_id":"fixture","code":0,"message":"","flash_result":[{"text":"recognized","channel_id":0,"sentence_list":[{"text":"recognized","start_time":0,"end_time":200,"word_list":[{"word":"recognized","start_time":150,"end_time":100}]}]}]}`,
			code: "invalid_word_timeline",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := testHTTPClient(func(*http.Request) (*http.Response, error) {
				return testResponse(http.StatusOK, []byte(test.body)), nil
			})
			provider, err := NewTencentFlashProvider(
				DefaultTencentFlashProfile("1234567890"),
				TencentCredentials{SecretID: "fixture-secret-id", SecretKey: "fixture-secret-key"},
				client,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Transcribe(context.Background(), Input{
				AssetID: "asset-fixture", DurationMS: 1,
				Audio: testAudio([]byte("audio"), "m4a", 16_000),
			})
			var providerError *ProviderError
			gotCode := ""
			if errors.As(err, &providerError) {
				gotCode = providerError.Code
			}
			if gotCode != test.code {
				t.Fatalf("Transcribe() error = %v, code = %q, want %q", err, gotCode, test.code)
			}
		})
	}
}

func TestTencentTemporaryHotwordCompilerIsStrictAndConservative(t *testing.T) {
	compiled, err := compileTencentHotwords([]Hotword{
		{Term: "low", Weight: 1},
		{Term: "middle", Weight: 50},
		{Term: "high", Weight: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if compiled != "low|1,middle|5,high|10" {
		t.Fatalf("compileTencentHotwords() = %q", compiled)
	}

	tests := []struct {
		name     string
		hotwords []Hotword
	}{
		{name: "delimiter", hotwords: []Hotword{{Term: "bad,term", Weight: 1}}},
		{name: "duplicate", hotwords: []Hotword{{Term: "Same", Weight: 1}, {Term: "same", Weight: 2}}},
		{name: "weight", hotwords: []Hotword{{Term: "term", Weight: 101}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := compileTencentHotwords(test.hotwords); err == nil {
				t.Fatal("compileTencentHotwords() unexpectedly accepted invalid input")
			}
		})
	}
}

func TestTencentFlashProviderRejectsUnsafeProfileAndUnsupportedCancel(t *testing.T) {
	profile := DefaultTencentFlashProfile("1234567890")
	profile.Endpoint = "http://127.0.0.1:8080"
	_, err := NewTencentFlashProvider(
		profile,
		TencentCredentials{SecretID: "fixture-secret-id", SecretKey: "fixture-secret-key"},
		nil,
	)
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("endpoint override error = %v, want ErrInvalidProfile", err)
	}

	profile = DefaultTencentFlashProfile("1234567890")
	profile.Model = "8k_zh"
	if _, err := NewTencentFlashProvider(
		profile,
		TencentCredentials{SecretID: "fixture-secret-id", SecretKey: "fixture-secret-key"},
		nil,
	); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("sample rate mismatch error = %v, want ErrInvalidProfile", err)
	}

	profile = DefaultTencentFlashProfile("1234567890")
	provider, err := NewTencentFlashProvider(
		profile,
		TencentCredentials{SecretID: "fixture-secret-id", SecretKey: "fixture-secret-key"},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Cancel(context.Background(), "task"); ErrorClassOf(err) != ErrorUnsupported || !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("Cancel() error = %v, want unsupported", err)
	}
}

func TestTencentCredentialsAreRedacted(t *testing.T) {
	credentials := TencentCredentials{SecretID: "fixture-secret-id", SecretKey: "fixture-secret-key"}
	formatted := fmt.Sprint(credentials)
	if formatted != "TencentCredentials{REDACTED}" || strings.Contains(formatted, "fixture-secret") {
		t.Fatalf("credentials formatted unsafely: %q", formatted)
	}
}
