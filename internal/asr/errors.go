package asr

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrorClass is stable, vendor-neutral, and safe to persist or expose in job
// state. Raw vendor messages are retained only in the immutable response body.
type ErrorClass string

const (
	ErrorInvalidConfiguration ErrorClass = "invalid_configuration"
	ErrorAuthentication       ErrorClass = "authentication"
	ErrorAuthorization        ErrorClass = "authorization"
	ErrorRateLimited          ErrorClass = "rate_limited"
	ErrorInvalidAudio         ErrorClass = "invalid_audio"
	ErrorUnsupported          ErrorClass = "unsupported"
	ErrorTransient            ErrorClass = "transient"
	ErrorRejected             ErrorClass = "rejected"
	ErrorCanceled             ErrorClass = "canceled"
)

var (
	ErrUnsupportedOperation = errors.New("ASR provider operation is unsupported")
	safeErrorCodePattern    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

// ProviderError intentionally omits the underlying message from Error so
// credentials echoed by a vendor cannot enter logs or API responses.
type ProviderError struct {
	Provider   string
	Operation  string
	Class      ErrorClass
	Code       string
	RetryAfter time.Duration
	cause      error
}

func (e *ProviderError) Error() string {
	code := e.Code
	if !safeErrorCodePattern.MatchString(code) {
		code = "unknown"
	}
	return fmt.Sprintf("%s %s failed (%s/%s)", e.Provider, e.Operation, e.Class, code)
}

func (e *ProviderError) Unwrap() error { return e.cause }

func newProviderError(provider, operation string, class ErrorClass, code string, cause error) error {
	return &ProviderError{
		Provider: provider, Operation: operation, Class: class, Code: code, cause: cause,
	}
}

// ErrorClassOf returns a stable class without exposing vendor details.
func ErrorClassOf(err error) ErrorClass {
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		return providerError.Class
	}
	return ""
}

// IsRetryable identifies failures eligible for bounded retry or failover.
func IsRetryable(err error) bool {
	switch ErrorClassOf(err) {
	case ErrorRateLimited, ErrorTransient:
		return true
	default:
		return false
	}
}
