package asr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxVendorExtensionBytes = 32 * 1024

var ErrInvalidProfile = errors.New("invalid ASR provider profile")

// Capabilities is the normalized feature and resource envelope exposed by an
// adapter. Values describe the implemented API mode, not every product a
// vendor may sell under the same brand.
type Capabilities struct {
	ProviderID          string   `json:"provider_id"`
	Batch               bool     `json:"batch"`
	Realtime            bool     `json:"realtime"`
	Sentence            bool     `json:"sentence"`
	Languages           []string `json:"languages"`
	Models              []string `json:"models"`
	Formats             []string `json:"formats"`
	SampleRates         []int    `json:"sample_rates"`
	Hotwords            bool     `json:"hotwords"`
	TemporaryHotwords   bool     `json:"temporary_hotwords"`
	Timestamps          bool     `json:"timestamps"`
	WordTimestamps      bool     `json:"word_timestamps"`
	SpeakerDiarization  bool     `json:"speaker_diarization"`
	Punctuation         bool     `json:"punctuation"`
	NumberNormalization bool     `json:"number_normalization"`
	MaxDurationMS       int64    `json:"max_duration_ms"`
	MaxFileSizeBytes    int64    `json:"max_file_size_bytes"`
	MaxConcurrency      int      `json:"max_concurrency"`
}

// RetryPolicy is persisted as part of a profile and interpreted by the worker.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// Profile is the provider-neutral configuration snapshot. Credentials are
// deliberately absent and are supplied through the server-side secret store.
type Profile struct {
	ID                  string
	ProviderID          string
	Endpoint            string
	Region              string
	Model               string
	Language            string
	Dialect             string
	SampleRate          int
	AudioFormat         string
	Punctuation         bool
	Timestamps          bool
	WordTimestamps      bool
	SpeakerDiarization  bool
	NumberNormalization bool
	HotwordSetID        string
	Timeout             time.Duration
	Retry               RetryPolicy
	Concurrency         int
	VendorExtension     json.RawMessage
}

// ValidateProfileAgainst applies provider-neutral validation. An adapter must
// additionally validate endpoint allowlists, model-specific features, and its
// vendor extension schema.
func ValidateProfileAgainst(profile Profile, capabilities Capabilities) error {
	if err := capabilities.Validate(); err != nil {
		return fmt.Errorf("%w: invalid capability model", ErrInvalidProfile)
	}
	if strings.TrimSpace(profile.ProviderID) != capabilities.ProviderID {
		return fmt.Errorf("%w: provider_id does not match adapter", ErrInvalidProfile)
	}
	if profile.SampleRate <= 0 || !containsInt(capabilities.SampleRates, profile.SampleRate) {
		return fmt.Errorf("%w: unsupported sample_rate", ErrInvalidProfile)
	}
	if !containsFold(capabilities.Formats, profile.AudioFormat) {
		return fmt.Errorf("%w: unsupported audio_format", ErrInvalidProfile)
	}
	if profile.Model != "" && len(capabilities.Models) > 0 &&
		!containsFold(capabilities.Models, profile.Model) {
		return fmt.Errorf("%w: unsupported model", ErrInvalidProfile)
	}
	if profile.Language != "" && len(capabilities.Languages) > 0 &&
		!containsFold(capabilities.Languages, profile.Language) &&
		!containsFold(capabilities.Languages, "*") {
		return fmt.Errorf("%w: unsupported language", ErrInvalidProfile)
	}
	if profile.WordTimestamps && !capabilities.WordTimestamps {
		return fmt.Errorf("%w: word timestamps are unavailable", ErrInvalidProfile)
	}
	if profile.Timestamps && !capabilities.Timestamps {
		return fmt.Errorf("%w: timestamps are unavailable", ErrInvalidProfile)
	}
	if profile.SpeakerDiarization && !capabilities.SpeakerDiarization {
		return fmt.Errorf("%w: speaker diarization is unavailable", ErrInvalidProfile)
	}
	if profile.Punctuation && !capabilities.Punctuation {
		return fmt.Errorf("%w: punctuation is unavailable", ErrInvalidProfile)
	}
	if profile.NumberNormalization && !capabilities.NumberNormalization {
		return fmt.Errorf("%w: number normalization is unavailable", ErrInvalidProfile)
	}
	if strings.TrimSpace(profile.HotwordSetID) != "" && !capabilities.Hotwords {
		return fmt.Errorf("%w: hotwords are unavailable", ErrInvalidProfile)
	}
	if profile.Timeout < time.Second || profile.Timeout > 2*time.Hour {
		return fmt.Errorf("%w: timeout must be between 1s and 2h", ErrInvalidProfile)
	}
	if profile.Concurrency < 1 || profile.Concurrency > capabilities.MaxConcurrency {
		return fmt.Errorf("%w: concurrency is outside provider limits", ErrInvalidProfile)
	}
	if profile.Retry.MaxAttempts < 1 || profile.Retry.MaxAttempts > 10 ||
		profile.Retry.BaseDelay < 100*time.Millisecond || profile.Retry.BaseDelay > 5*time.Minute ||
		profile.Retry.MaxDelay < profile.Retry.BaseDelay || profile.Retry.MaxDelay > 30*time.Minute {
		return fmt.Errorf("%w: retry policy is outside safe limits", ErrInvalidProfile)
	}
	if err := validateVendorExtension(profile.VendorExtension); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidProfile, err)
	}
	return nil
}

// Validate rejects incomplete or internally inconsistent capability models.
func (capabilities Capabilities) Validate() error {
	if strings.TrimSpace(capabilities.ProviderID) == "" || !capabilities.Batch {
		return errors.New("provider_id is empty or batch transcription is disabled")
	}
	if len(capabilities.Formats) == 0 || len(capabilities.SampleRates) == 0 {
		return errors.New("formats or sample rates are empty")
	}
	if capabilities.MaxDurationMS <= 0 || capabilities.MaxFileSizeBytes <= 0 ||
		capabilities.MaxConcurrency <= 0 {
		return errors.New("resource limits must be positive")
	}
	if capabilities.WordTimestamps && !capabilities.Timestamps {
		return errors.New("word timestamps require timestamps")
	}
	return nil
}

func validateVendorExtension(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	if len(trimmed) > maxVendorExtensionBytes || trimmed[0] != '{' || !json.Valid(trimmed) {
		return errors.New("vendor_extension must be a JSON object no larger than 32 KiB")
	}
	return nil
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
