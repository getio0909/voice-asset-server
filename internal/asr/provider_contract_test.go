package asr

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func testHTTPClient(function roundTripFunc) *http.Client {
	return &http.Client{Transport: function}
}

func testAudio(data []byte, format string, sampleRate int) *Audio {
	return &Audio{
		Open: func(ctx context.Context) (io.ReadCloser, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return io.NopCloser(bytes.NewReader(data)), nil
		},
		SizeBytes: int64(len(data)), Format: format, SampleRate: sampleRate,
	}
}

func testResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func TestProviderErrorsAreClassifiedWithoutLeakingCauses(t *testing.T) {
	secret := "credential-value-that-must-not-leak"
	err := newProviderError("fixture", "transcribe", ErrorTransient, "transport", errors.New(secret))
	if ErrorClassOf(err) != ErrorTransient || !IsRetryable(err) {
		t.Fatalf("error classification = %q, retryable %t", ErrorClassOf(err), IsRetryable(err))
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("provider error exposed its underlying cause")
	}

	nonRetryable := newProviderError("fixture", "transcribe", ErrorAuthentication, "bad-code value", nil)
	if IsRetryable(nonRetryable) {
		t.Fatal("authentication error was marked retryable")
	}
	if strings.Contains(nonRetryable.Error(), "bad-code value") {
		t.Fatal("unsafe vendor error code was exposed")
	}
}

func TestProfileValidationRejectsUnsupportedAndUnboundedValues(t *testing.T) {
	profile := DefaultAliyunFlashProfile()
	profile.VendorExtension = []byte(`{"appkey":"fixture-appkey"}`)
	provider, err := NewAliyunFlashProvider(profile, AliyunCredentials{AccessToken: "fixture-token"}, nil)
	if err != nil {
		t.Fatalf("NewAliyunFlashProvider() error = %v", err)
	}

	unsupported := profile
	unsupported.AudioFormat = "flac"
	if err := provider.ValidateProfile(unsupported); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("unsupported format error = %v, want ErrInvalidProfile", err)
	}

	invalidExtension := profile
	invalidExtension.VendorExtension = []byte(`[]`)
	if err := provider.ValidateProfile(invalidExtension); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("non-object extension error = %v, want ErrInvalidProfile", err)
	}

	invalidRetry := profile
	invalidRetry.Retry.MaxAttempts = 0
	if err := provider.ValidateProfile(invalidRetry); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("invalid retry error = %v, want ErrInvalidProfile", err)
	}
}
