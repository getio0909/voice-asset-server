package llm

import (
	"errors"
	"fmt"
	"regexp"
)

type ErrorClass string

const (
	ErrorInvalidConfiguration ErrorClass = "invalid_configuration"
	ErrorAuthentication       ErrorClass = "authentication"
	ErrorAuthorization        ErrorClass = "authorization"
	ErrorRateLimited          ErrorClass = "rate_limited"
	ErrorTransient            ErrorClass = "transient"
	ErrorRejected             ErrorClass = "rejected"
	ErrorUnsafeProposal       ErrorClass = "unsafe_proposal"
	ErrorCanceled             ErrorClass = "canceled"
)

var safeErrorCodePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type ProviderError struct {
	Provider  string
	Operation string
	Class     ErrorClass
	Code      string
	cause     error
}

func (providerError *ProviderError) Error() string {
	code := providerError.Code
	if !safeErrorCodePattern.MatchString(code) {
		code = "unknown"
	}
	return fmt.Sprintf("%s %s failed (%s/%s)", providerError.Provider, providerError.Operation, providerError.Class, code)
}

func (providerError *ProviderError) Unwrap() error { return providerError.cause }

func newProviderError(provider, operation string, class ErrorClass, code string, cause error) error {
	return &ProviderError{
		Provider: provider, Operation: operation, Class: class, Code: code, cause: cause,
	}
}

func ErrorClassOf(err error) ErrorClass {
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		return providerError.Class
	}
	return ""
}

func IsRetryable(err error) bool {
	switch ErrorClassOf(err) {
	case ErrorRateLimited, ErrorTransient:
		return true
	default:
		return false
	}
}
