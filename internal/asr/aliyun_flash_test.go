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
)

func TestAliyunFlashProviderMapsFixtureAndPreservesRawResponse(t *testing.T) {
	fixture, err := os.ReadFile("testdata/aliyun_flash_success.json")
	if err != nil {
		t.Fatal(err)
	}
	audioBytes := []byte("fixture-m4a-audio")
	client := testHTTPClient(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Host != "nls-gateway-cn-shanghai.aliyuncs.com" ||
			request.URL.Path != aliyunFlashPath {
			t.Fatalf("unexpected Aliyun request: %s %s%s", request.Method, request.URL.Host, request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("appkey") != "fixture-appkey" || query.Get("token") != "fixture-token" ||
			query.Get("format") != "mp4" || query.Get("sample_rate") != "16000" {
			t.Fatal("Aliyun request query did not match the validated profile")
		}
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(body, audioBytes) || request.ContentLength != int64(len(audioBytes)) {
			t.Fatal("Aliyun request did not stream the original audio bytes")
		}
		return testResponse(http.StatusOK, fixture), nil
	})
	profile := DefaultAliyunFlashProfile()
	profile.VendorExtension = []byte(`{"appkey":"fixture-appkey"}`)
	provider, err := NewAliyunFlashProvider(
		profile, AliyunCredentials{AccessToken: "fixture-token"}, client,
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := provider.Transcribe(context.Background(), Input{
		AssetID: "asset-fixture", Language: "zh-CN", DurationMS: 2386,
		Audio: testAudio(audioBytes, "m4a", 16_000),
	})
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if result.RawSchema != RawSchemaAliyunFlashV1 || !bytes.Equal(result.RawJSON, fixture) {
		t.Fatal("Aliyun raw response was not preserved byte-for-byte")
	}
	if result.Text != "语音资产欢迎您。" || len(result.Segments) != 1 ||
		result.Segments[0].Speaker != "channel-0" || len(result.Segments[0].Words) != 2 ||
		result.Segments[0].Words[1].Text != "欢迎您。" {
		t.Fatalf("unexpected normalized Aliyun result: %+v", result)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("Result.Validate() error = %v", err)
	}
}

func TestAliyunFlashProviderClassifiesVendorFailureWithoutLeakingMessage(t *testing.T) {
	const vendorMessage = "quota exhausted for credential fixture-secret"
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		body := []byte(`{"task_id":"fixture","status":40000005,"message":"` + vendorMessage + `"}`)
		return testResponse(http.StatusOK, body), nil
	})
	profile := DefaultAliyunFlashProfile()
	profile.VendorExtension = []byte(`{"appkey":"fixture-appkey"}`)
	provider, err := NewAliyunFlashProvider(profile, AliyunCredentials{AccessToken: "fixture-token"}, client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Transcribe(context.Background(), Input{
		AssetID: "asset-fixture", DurationMS: 1,
		Audio: testAudio([]byte("audio"), "m4a", 16_000),
	})
	if ErrorClassOf(err) != ErrorRateLimited || !IsRetryable(err) {
		t.Fatalf("Transcribe() error = %v, want retryable rate limit", err)
	}
	if strings.Contains(err.Error(), vendorMessage) || strings.Contains(err.Error(), "fixture-token") {
		t.Fatal("Aliyun error exposed a vendor message or token")
	}
}

func TestAliyunFlashProviderRejectsEndpointOverrideAndUnsupportedCancel(t *testing.T) {
	profile := DefaultAliyunFlashProfile()
	profile.Endpoint = "http://127.0.0.1:8080"
	profile.VendorExtension = []byte(`{"appkey":"fixture-appkey"}`)
	_, err := NewAliyunFlashProvider(profile, AliyunCredentials{AccessToken: "fixture-token"}, nil)
	if !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("endpoint override error = %v, want ErrInvalidProfile", err)
	}

	profile.Endpoint = ""
	provider, err := NewAliyunFlashProvider(profile, AliyunCredentials{AccessToken: "fixture-token"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.Cancel(context.Background(), "task"); ErrorClassOf(err) != ErrorUnsupported || !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("Cancel() error = %v, want unsupported", err)
	}
}

func TestAliyunCredentialsAreRedacted(t *testing.T) {
	credentials := AliyunCredentials{AccessKeyID: "fixture-access-id", AccessKeySecret: "fixture-access-secret"}
	formatted := fmt.Sprint(credentials)
	if formatted != "AliyunCredentials{REDACTED}" || strings.Contains(formatted, "fixture-access") {
		t.Fatalf("credentials formatted unsafely: %q", formatted)
	}
}
