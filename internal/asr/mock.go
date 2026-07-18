package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const chineseFixture = `{"language":"zh-CN","text":"欢迎使用语音资产。","segments":[{"id":"segment-0001","start_ms":0,"end_ms":450,"speaker":"speaker-1","text":"欢迎使用","confidence":0.99,"words":[{"start_ms":0,"end_ms":220,"text":"欢迎","confidence":0.99},{"start_ms":220,"end_ms":450,"text":"使用","confidence":0.98}]},{"id":"segment-0002","start_ms":450,"end_ms":1000,"speaker":"speaker-1","text":"语音资产。","confidence":0.98,"words":[{"start_ms":450,"end_ms":650,"text":"语音","confidence":0.98},{"start_ms":650,"end_ms":900,"text":"资产","confidence":0.97},{"start_ms":900,"end_ms":1000,"text":"。","confidence":0.99}]}]}`

const englishFixture = `{"language":"en-US","text":"Welcome to VoiceAsset.","segments":[{"id":"segment-0001","start_ms":0,"end_ms":500,"speaker":"speaker-1","text":"Welcome to","confidence":0.99,"words":[{"start_ms":0,"end_ms":350,"text":"Welcome","confidence":0.99},{"start_ms":350,"end_ms":500,"text":"to","confidence":0.98}]},{"id":"segment-0002","start_ms":500,"end_ms":1000,"speaker":"speaker-1","text":"VoiceAsset.","confidence":0.98,"words":[{"start_ms":500,"end_ms":900,"text":"VoiceAsset","confidence":0.98},{"start_ms":900,"end_ms":1000,"text":".","confidence":0.99}]}]}`

// MockProvider is a stateless deterministic ASR provider for development and
// tests. Its output depends only on whether the language is Chinese.
type MockProvider struct{}

var _ Provider = (*MockProvider)(nil)

// NewMockProvider returns a deterministic provider with no external
// configuration or credentials.
func NewMockProvider() Provider {
	return &MockProvider{}
}

func (*MockProvider) ID() string { return "mock_asr" }

func (*MockProvider) Capabilities() Capabilities {
	return Capabilities{
		ProviderID: "mock_asr", Batch: true,
		Languages: []string{"*"}, Models: []string{"deterministic_fixture"},
		Formats: []string{"wav", "m4a"}, SampleRates: []int{8_000, 16_000, 44_100, 48_000},
		Timestamps: true, WordTimestamps: true, Punctuation: true,
		MaxDurationMS: 12 * 60 * 60 * 1_000, MaxFileSizeBytes: 512 * 1024 * 1024,
		MaxConcurrency: 128,
	}
}

func (provider *MockProvider) ValidateProfile(profile Profile) error {
	if strings.TrimSpace(profile.Endpoint) != "" || strings.TrimSpace(profile.Region) != "" {
		return fmt.Errorf("%w: mock endpoint and region must be empty", ErrInvalidProfile)
	}
	trimmedExtension := bytes.TrimSpace(profile.VendorExtension)
	if len(trimmedExtension) > 0 && !bytes.Equal(trimmedExtension, []byte(`{}`)) {
		var extension map[string]json.RawMessage
		if json.Unmarshal(trimmedExtension, &extension) != nil || len(extension) != 0 {
			return fmt.Errorf("%w: mock vendor_extension must be empty", ErrInvalidProfile)
		}
	}
	return ValidateProfileAgainst(profile, provider.Capabilities())
}

func (*MockProvider) Health(ctx context.Context) error { return ctx.Err() }

func (*MockProvider) Cancel(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return newProviderError("mock_asr", "cancel", ErrorUnsupported, "synchronous", ErrUnsupportedOperation)
}

// Transcribe returns a fresh decoding of a fixed fixture so callers cannot
// mutate shared state and change later replays.
func (*MockProvider) Transcribe(ctx context.Context, input Input) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	fixture := englishFixture
	if isChinese(input.Language) {
		fixture = chineseFixture
	}
	result, err := resultFromRaw(fixture)
	if err != nil {
		return Result{}, fmt.Errorf("decode mock ASR fixture: %w", err)
	}
	result.ProviderID = "mock_asr"

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func isChinese(language string) bool {
	language = strings.ToLower(strings.TrimSpace(language))
	return language == "zh" || strings.HasPrefix(language, "zh-") || strings.HasPrefix(language, "zh_")
}
