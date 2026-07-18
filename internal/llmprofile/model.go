// Package llmprofile manages encrypted workspace-scoped LLM provider
// configurations without returning secret values from public models.
package llmprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/llm"
)

const (
	StateEnabled  = "enabled"
	StateDisabled = "disabled"
)

var (
	ErrForbidden             = errors.New("LLM profile access forbidden")
	ErrInvalidInput          = errors.New("invalid LLM profile input")
	ErrConflict              = errors.New("LLM profile conflict")
	ErrNotFound              = errors.New("LLM profile not found")
	ErrEncryptionUnavailable = errors.New("LLM profile encryption unavailable")
	ErrConfiguration         = errors.New("LLM profile configuration failed")
)

// Config contains only non-secret values. Custom header values are stored in
// encrypted credentials; this public representation exposes their names only.
type Config struct {
	BaseURL            string   `json:"base_url,omitempty"`
	Model              string   `json:"model"`
	CustomHeaderNames  []string `json:"custom_header_names,omitempty"`
	Timeout            string   `json:"timeout"`
	Concurrency        int      `json:"concurrency"`
	Temperature        float64  `json:"temperature"`
	ContextLimit       int      `json:"context_limit"`
	StructuredOutput   bool     `json:"structured_output"`
	PromptTemplate     string   `json:"prompt_template"`
	DefaultGlossaryID  string   `json:"default_glossary_id,omitempty"`
	AutoApprovalPolicy string   `json:"auto_approval_policy"`
}

func (config Config) ProviderProfile(id, providerID string) (llm.Profile, error) {
	timeout, err := time.ParseDuration(strings.TrimSpace(config.Timeout))
	if err != nil {
		return llm.Profile{}, fmt.Errorf("%w: invalid timeout", ErrInvalidInput)
	}
	profile := llm.Profile{
		ID: id, ProviderID: providerID, BaseURL: strings.TrimSpace(config.BaseURL),
		Model: strings.TrimSpace(config.Model), Timeout: timeout,
		Concurrency: config.Concurrency, Temperature: config.Temperature,
		ContextLimit: config.ContextLimit, StructuredOutput: config.StructuredOutput,
		PromptTemplate:     strings.TrimSpace(config.PromptTemplate),
		DefaultGlossaryID:  strings.TrimSpace(config.DefaultGlossaryID),
		AutoApprovalPolicy: strings.TrimSpace(config.AutoApprovalPolicy),
	}
	return profile, nil
}

type Profile struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	ProviderID       string    `json:"provider_id"`
	DisplayName      string    `json:"display_name"`
	Config           Config    `json:"config"`
	State            string    `json:"state"`
	Priority         int       `json:"priority"`
	Version          int64     `json:"version"`
	SecretConfigured bool      `json:"secret_configured"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type StoredProfile struct {
	Profile
	SecretCiphertext []byte
}

type CreateInput struct {
	ProviderID  string          `json:"provider_id"`
	DisplayName string          `json:"display_name"`
	Config      Config          `json:"config"`
	Credentials json.RawMessage `json:"credentials,omitempty"`
	State       string          `json:"state,omitempty"`
	Priority    int             `json:"priority,omitempty"`
}

type UpdateInput struct {
	DisplayName *string         `json:"display_name,omitempty"`
	Config      *Config         `json:"config,omitempty"`
	Credentials json.RawMessage `json:"credentials,omitempty"`
	State       *string         `json:"state,omitempty"`
	Priority    *int            `json:"priority,omitempty"`
}

type Health struct {
	ProfileID  string         `json:"profile_id"`
	Status     string         `json:"status"`
	ErrorClass llm.ErrorClass `json:"error_class,omitempty"`
	CheckedAt  time.Time      `json:"checked_at"`
}

func decodeConfig(raw []byte) (Config, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("LLM config contains trailing JSON")
	}
	return config, nil
}
