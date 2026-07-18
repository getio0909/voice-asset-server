package asr

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxFlashAudioBytes       = 100 * 1024 * 1024
	maxProviderResponseBytes = 16 * 1024 * 1024
)

func providerHTTPClient(source *http.Client) *http.Client {
	if source == nil {
		source = http.DefaultClient
	}
	client := *source
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &client
}

func readProviderResponse(body io.ReadCloser) ([]byte, error) {
	defer body.Close()
	limited := &io.LimitedReader{R: body, N: maxProviderResponseBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxProviderResponseBytes {
		return nil, errors.New("provider response exceeds 16 MiB")
	}
	return data, nil
}

func httpStatusError(provider, operation string, response *http.Response) error {
	class := ErrorRejected
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		class = ErrorAuthentication
	case response.StatusCode == http.StatusForbidden:
		class = ErrorAuthorization
	case response.StatusCode == http.StatusTooManyRequests:
		class = ErrorRateLimited
	case response.StatusCode == http.StatusRequestTimeout || response.StatusCode >= 500:
		class = ErrorTransient
	}
	errorValue := &ProviderError{
		Provider: provider, Operation: operation, Class: class,
		Code: fmt.Sprintf("http_%d", response.StatusCode),
	}
	if class == ErrorRateLimited {
		errorValue.RetryAfter = parseRetryAfter(response.Header.Get("Retry-After"), time.Now())
	}
	return errorValue
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if parsed, err := http.ParseTime(value); err == nil && parsed.After(now) {
		return parsed.Sub(now)
	}
	return 0
}
