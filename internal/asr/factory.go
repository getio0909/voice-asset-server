package asr

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const MockProviderID = "mock_asr"

// BuiltInCapabilities returns detached capability models in stable provider
// order without requiring configuration or credentials.
func BuiltInCapabilities() []Capabilities {
	return []Capabilities{
		cloneCapabilities((&MockProvider{}).Capabilities()),
		cloneCapabilities((&AliyunFlashProvider{}).Capabilities()),
		cloneCapabilities((&TencentFlashProvider{}).Capabilities()),
	}
}

// DefaultMockProfile returns a persisted profile compatible with the
// deterministic provider. The ID is supplied by the caller's identifier
// boundary rather than generated in the adapter.
func DefaultMockProfile(id string) Profile {
	return Profile{
		ID: id, ProviderID: MockProviderID, Model: "deterministic_fixture", Language: "zh-CN",
		SampleRate: 16_000, AudioFormat: "wav", Punctuation: true,
		Timestamps: true, WordTimestamps: true, Timeout: time.Minute,
		Retry:       RetryPolicy{MaxAttempts: 1, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second},
		Concurrency: 32, VendorExtension: json.RawMessage(`{}`),
	}
}

// ValidateProfileDefinition validates a credential-free profile against its
// selected built-in adapter.
func ValidateProfileDefinition(profile Profile) error {
	switch strings.TrimSpace(profile.ProviderID) {
	case MockProviderID:
		return (&MockProvider{}).ValidateProfile(profile)
	case AliyunProviderID:
		return (&AliyunFlashProvider{}).ValidateProfile(profile)
	case TencentProviderID:
		return (&TencentFlashProvider{}).ValidateProfile(profile)
	default:
		return newProviderError("asr_factory", "configure", ErrorUnsupported, "unknown_provider", nil)
	}
}

// NewConfiguredProvider strictly decodes the provider-specific credential
// object and returns a validated adapter without performing a network request.
func NewConfiguredProvider(
	profile Profile,
	secretJSON json.RawMessage,
	client *http.Client,
) (Provider, error) {
	if err := ValidateProfileDefinition(profile); err != nil {
		return nil, err
	}
	switch profile.ProviderID {
	case MockProviderID:
		if err := validateEmptyProviderSecret(secretJSON); err != nil {
			return nil, err
		}
		return &MockProvider{}, nil
	case AliyunProviderID:
		var secret struct {
			AccessKeyID     string `json:"access_key_id,omitempty"`
			AccessKeySecret string `json:"access_key_secret,omitempty"`
			AccessToken     string `json:"access_token,omitempty"`
		}
		if err := decodeProviderSecret(secretJSON, &secret); err != nil {
			return nil, err
		}
		return NewAliyunFlashProvider(profile, AliyunCredentials{
			AccessKeyID: secret.AccessKeyID, AccessKeySecret: secret.AccessKeySecret, AccessToken: secret.AccessToken,
		}, client)
	case TencentProviderID:
		var secret struct {
			SecretID  string `json:"secret_id"`
			SecretKey string `json:"secret_key"`
		}
		if err := decodeProviderSecret(secretJSON, &secret); err != nil {
			return nil, err
		}
		return NewTencentFlashProvider(profile, TencentCredentials{
			SecretID: secret.SecretID, SecretKey: secret.SecretKey,
		}, client)
	default:
		return nil, newProviderError("asr_factory", "configure", ErrorUnsupported, "unknown_provider", nil)
	}
}

func decodeProviderSecret(raw json.RawMessage, target any) error {
	if err := decodeStrictObject(raw, target); err != nil {
		return newProviderError("asr_factory", "configure", ErrorInvalidConfiguration, "invalid_secret", err)
	}
	return nil
}

func validateEmptyProviderSecret(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	var secret map[string]json.RawMessage
	if err := decodeStrictObject(trimmed, &secret); err != nil || len(secret) != 0 {
		if err == nil {
			err = errors.New("mock credentials are not empty")
		}
		return newProviderError("asr_factory", "configure", ErrorInvalidConfiguration, "unexpected_secret", err)
	}
	return nil
}
