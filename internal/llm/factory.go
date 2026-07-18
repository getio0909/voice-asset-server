package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func BuiltInCapabilities() []Capabilities {
	return []Capabilities{
		(&MockProvider{}).Capabilities(),
		(&OpenAICompatibleProvider{}).Capabilities(),
	}
}

func NewConfiguredProvider(
	profile Profile,
	secretJSON json.RawMessage,
	client *http.Client,
) (Provider, error) {
	if err := ValidateProfileDefinition(profile); err != nil {
		return nil, newProviderError(profile.ProviderID, "configure", ErrorInvalidConfiguration, "profile", err)
	}
	switch profile.ProviderID {
	case MockProviderID:
		if err := validateEmptySecret(secretJSON); err != nil {
			return nil, err
		}
		return NewMockProvider(profile)
	case OpenAICompatibleProviderID:
		var secret struct {
			APIKey        string            `json:"api_key"`
			CustomHeaders map[string]string `json:"custom_headers,omitempty"`
		}
		if err := decodeSecret(secretJSON, &secret); err != nil {
			return nil, err
		}
		profile.CustomHeaders = secret.CustomHeaders
		return NewOpenAICompatibleProvider(profile, Credentials{APIKey: secret.APIKey}, client)
	default:
		return nil, newProviderError(profile.ProviderID, "configure", ErrorInvalidConfiguration, "provider_id", nil)
	}
}

func decodeSecret(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || len(trimmed) > 64*1024 || trimmed[0] != '{' {
		return newProviderError("llm_factory", "configure", ErrorInvalidConfiguration, "secret", nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return newProviderError("llm_factory", "configure", ErrorInvalidConfiguration, "secret", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return newProviderError("llm_factory", "configure", ErrorInvalidConfiguration, "secret", err)
	}
	return nil
}

func validateEmptySecret(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := decodeSecret(trimmed, &fields); err != nil || len(fields) != 0 {
		return newProviderError(MockProviderID, "configure", ErrorInvalidConfiguration, "unexpected_secret", err)
	}
	return nil
}
