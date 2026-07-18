// Package providerprofile manages workspace-scoped, encrypted provider
// configurations without exposing credentials through public models.
package providerprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
)

const (
	StateEnabled  = "enabled"
	StateDisabled = "disabled"
)

var (
	ErrForbidden             = errors.New("provider profile access forbidden")
	ErrInvalidInput          = errors.New("invalid provider profile input")
	ErrConflict              = errors.New("provider profile conflict")
	ErrNotFound              = errors.New("provider profile not found")
	ErrEncryptionUnavailable = errors.New("provider profile encryption unavailable")
	ErrConfiguration         = errors.New("provider profile configuration failed")
)

type RetryConfig struct {
	MaxAttempts int    `json:"max_attempts"`
	BaseDelay   string `json:"base_delay"`
	MaxDelay    string `json:"max_delay"`
}

// ASRConfig is the credential-free JSON document persisted in config.
type ASRConfig struct {
	Endpoint            string          `json:"endpoint,omitempty"`
	Region              string          `json:"region,omitempty"`
	Model               string          `json:"model"`
	Language            string          `json:"language"`
	Dialect             string          `json:"dialect,omitempty"`
	SampleRate          int             `json:"sample_rate"`
	AudioFormat         string          `json:"audio_format"`
	Punctuation         bool            `json:"punctuation"`
	Timestamps          bool            `json:"timestamps"`
	WordTimestamps      bool            `json:"word_timestamps"`
	SpeakerDiarization  bool            `json:"speaker_diarization"`
	NumberNormalization bool            `json:"number_normalization"`
	HotwordSetID        string          `json:"hotword_set_id,omitempty"`
	Timeout             string          `json:"timeout"`
	Retry               RetryConfig     `json:"retry"`
	Concurrency         int             `json:"concurrency"`
	VendorExtension     json.RawMessage `json:"vendor_extension"`
}

func (config ASRConfig) ASRProfile(id, providerID string) (asr.Profile, error) {
	timeout, err := time.ParseDuration(strings.TrimSpace(config.Timeout))
	if err != nil {
		return asr.Profile{}, fmt.Errorf("%w: invalid timeout", ErrInvalidInput)
	}
	baseDelay, err := time.ParseDuration(strings.TrimSpace(config.Retry.BaseDelay))
	if err != nil {
		return asr.Profile{}, fmt.Errorf("%w: invalid retry base delay", ErrInvalidInput)
	}
	maxDelay, err := time.ParseDuration(strings.TrimSpace(config.Retry.MaxDelay))
	if err != nil {
		return asr.Profile{}, fmt.Errorf("%w: invalid retry max delay", ErrInvalidInput)
	}
	profile := asr.Profile{
		ID: id, ProviderID: providerID,
		Endpoint: config.Endpoint, Region: config.Region, Model: config.Model,
		Language: config.Language, Dialect: config.Dialect,
		SampleRate: config.SampleRate, AudioFormat: config.AudioFormat,
		Punctuation: config.Punctuation, Timestamps: config.Timestamps,
		WordTimestamps: config.WordTimestamps, SpeakerDiarization: config.SpeakerDiarization,
		NumberNormalization: config.NumberNormalization, HotwordSetID: config.HotwordSetID,
		Timeout: timeout,
		Retry: asr.RetryPolicy{
			MaxAttempts: config.Retry.MaxAttempts, BaseDelay: baseDelay, MaxDelay: maxDelay,
		},
		Concurrency:     config.Concurrency,
		VendorExtension: append(json.RawMessage(nil), config.VendorExtension...),
	}
	return profile, nil
}

type Profile struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	ProviderID       string    `json:"provider_id"`
	DisplayName      string    `json:"display_name"`
	Config           ASRConfig `json:"config"`
	State            string    `json:"state"`
	Priority         int       `json:"priority"`
	Version          int64     `json:"version"`
	SecretConfigured bool      `json:"secret_configured"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// StoredProfile is internal worker material. SecretCiphertext has no JSON tag
// and must never be passed to an HTTP response.
type StoredProfile struct {
	Profile
	SecretCiphertext []byte
}

type CreateInput struct {
	ProviderID  string          `json:"provider_id"`
	DisplayName string          `json:"display_name"`
	Config      ASRConfig       `json:"config"`
	Credentials json.RawMessage `json:"credentials,omitempty"`
	State       string          `json:"state,omitempty"`
	Priority    int             `json:"priority,omitempty"`
}

type UpdateInput struct {
	DisplayName *string         `json:"display_name,omitempty"`
	Config      *ASRConfig      `json:"config,omitempty"`
	Credentials json.RawMessage `json:"credentials,omitempty"`
	State       *string         `json:"state,omitempty"`
	Priority    *int            `json:"priority,omitempty"`
}

type Health struct {
	ProfileID  string         `json:"profile_id"`
	Status     string         `json:"status"`
	ErrorClass asr.ErrorClass `json:"error_class,omitempty"`
	CheckedAt  time.Time      `json:"checked_at"`
}

func decodeConfig(raw []byte) (ASRConfig, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config ASRConfig
	if err := decoder.Decode(&config); err != nil {
		return ASRConfig{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ASRConfig{}, errors.New("config contains multiple JSON values")
		}
		return ASRConfig{}, err
	}
	return config, nil
}
