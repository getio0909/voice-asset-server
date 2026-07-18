package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	AliyunProviderID       = "aliyun_asr"
	aliyunFlashEndpoint    = "https://nls-gateway-cn-shanghai.aliyuncs.com"
	aliyunFlashPath        = "/stream/v1/FlashRecognizer"
	aliyunFlashSuccessCode = 20_000_000
)

// AliyunCredentials selects either a renewable AccessKey flow or a temporary
// NLS token. Values are never included in JSON or error messages.
type AliyunCredentials struct {
	AccessKeyID     string `json:"-"`
	AccessKeySecret string `json:"-"`
	AccessToken     string `json:"-"`
}

func (AliyunCredentials) String() string { return "AliyunCredentials{REDACTED}" }

type aliyunVendorExtension struct {
	AppKey               string   `json:"appkey"`
	VocabularyID         string   `json:"vocabulary_id,omitempty"`
	CustomizationID      string   `json:"customization_id,omitempty"`
	FirstChannelOnly     *bool    `json:"first_channel_only,omitempty"`
	SpeechNoiseThreshold *float64 `json:"speech_noise_threshold,omitempty"`
	SentenceMaxLength    int      `json:"sentence_max_length,omitempty"`
}

// AliyunFlashProvider implements the documented synchronous recording-file
// express API and never requires a public audio URL.
type AliyunFlashProvider struct {
	profile   Profile
	extension aliyunVendorExtension
	tokens    aliyunTokenSource
	client    *http.Client
	endpoint  string
}

var _ Provider = (*AliyunFlashProvider)(nil)

func DefaultAliyunFlashProfile() Profile {
	return Profile{
		ProviderID: AliyunProviderID, Region: "cn-shanghai",
		Model: "project_configured", Language: "zh-CN",
		SampleRate: 16_000, AudioFormat: "m4a",
		Punctuation: true, Timestamps: true, WordTimestamps: true,
		NumberNormalization: true, Timeout: 2 * time.Minute,
		Retry:       RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 30 * time.Second},
		Concurrency: 2,
	}
}

func NewAliyunFlashProvider(
	profile Profile,
	credentials AliyunCredentials,
	client *http.Client,
) (*AliyunFlashProvider, error) {
	provider := &AliyunFlashProvider{
		profile: profile, client: providerHTTPClient(client), endpoint: aliyunFlashEndpoint,
	}
	if err := provider.ValidateProfile(profile); err != nil {
		return nil, err
	}
	extension, err := decodeAliyunExtension(profile.VendorExtension)
	if err != nil {
		return nil, err
	}
	provider.extension = extension

	hasToken := strings.TrimSpace(credentials.AccessToken) != ""
	hasAccessKey := strings.TrimSpace(credentials.AccessKeyID) != "" ||
		strings.TrimSpace(credentials.AccessKeySecret) != ""
	switch {
	case hasToken && !hasAccessKey && validOpaqueCredential(credentials.AccessToken):
		provider.tokens = aliyunStaticTokenSource{token: credentials.AccessToken}
	case !hasToken && hasAccessKey:
		provider.tokens, err = newAliyunAccessKeyTokenSource(
			credentials.AccessKeyID, credentials.AccessKeySecret, client,
		)
		if err != nil {
			return nil, err
		}
	default:
		return nil, newProviderError(
			AliyunProviderID, "configure", ErrorInvalidConfiguration, "credential_scheme", nil,
		)
	}
	return provider, nil
}

func (*AliyunFlashProvider) ID() string { return AliyunProviderID }

func (*AliyunFlashProvider) Capabilities() Capabilities {
	return Capabilities{
		ProviderID: AliyunProviderID, Batch: true,
		Languages: []string{"*"}, Models: []string{"project_configured"},
		Formats:     []string{"wav", "m4a", "mp4", "aac", "mp3", "opus"},
		SampleRates: []int{8_000, 16_000}, Hotwords: true,
		Timestamps: true, WordTimestamps: true, Punctuation: true,
		NumberNormalization: true, MaxDurationMS: 2 * 60 * 60 * 1_000,
		MaxFileSizeBytes: maxFlashAudioBytes, MaxConcurrency: 2,
	}
}

func (provider *AliyunFlashProvider) ValidateProfile(profile Profile) error {
	if err := ValidateProfileAgainst(profile, provider.Capabilities()); err != nil {
		return err
	}
	endpoint := strings.TrimSuffix(strings.TrimSpace(profile.Endpoint), "/")
	if endpoint != "" && endpoint != aliyunFlashEndpoint {
		return fmt.Errorf("%w: endpoint is not the official Aliyun gateway", ErrInvalidProfile)
	}
	if strings.TrimSpace(profile.Region) != "cn-shanghai" {
		return fmt.Errorf("%w: region must be cn-shanghai for this adapter", ErrInvalidProfile)
	}
	if strings.TrimSpace(profile.Language) == "" {
		return fmt.Errorf("%w: language must not be empty", ErrInvalidProfile)
	}
	_, err := decodeAliyunExtension(profile.VendorExtension)
	return err
}

func (provider *AliyunFlashProvider) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := provider.tokens.Token(ctx)
	return err
}

func (*AliyunFlashProvider) Cancel(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return newProviderError(
		AliyunProviderID, "cancel", ErrorUnsupported, "synchronous", ErrUnsupportedOperation,
	)
}

func (provider *AliyunFlashProvider) Transcribe(ctx context.Context, input Input) (Result, error) {
	if err := validateFlashInput(input, provider.profile, provider.Capabilities()); err != nil {
		return Result{}, newProviderError(AliyunProviderID, "transcribe", ErrorInvalidAudio, "invalid_input", err)
	}
	if len(input.Hotwords) > 0 {
		return Result{}, newProviderError(
			AliyunProviderID, "transcribe", ErrorUnsupported, "temporary_hotwords", nil,
		)
	}
	requestContext, cancel := context.WithTimeout(ctx, provider.profile.Timeout)
	defer cancel()
	token, err := provider.tokens.Token(requestContext)
	if err != nil {
		return Result{}, err
	}

	query := url.Values{}
	query.Set("appkey", provider.extension.AppKey)
	query.Set("token", token)
	query.Set("format", aliyunAudioFormat(input.Audio.Format))
	query.Set("sample_rate", strconv.Itoa(provider.profile.SampleRate))
	query.Set("enable_inverse_text_normalization", strconv.FormatBool(provider.profile.NumberNormalization))
	query.Set("enable_word_level_result", strconv.FormatBool(provider.profile.WordTimestamps))
	query.Set("enable_timestamp_alignment", strconv.FormatBool(provider.profile.Timestamps))
	if provider.extension.VocabularyID != "" {
		query.Set("vocabulary_id", provider.extension.VocabularyID)
	}
	if provider.extension.CustomizationID != "" {
		query.Set("customization_id", provider.extension.CustomizationID)
	}
	if provider.extension.FirstChannelOnly != nil {
		query.Set("first_channel_only", strconv.FormatBool(*provider.extension.FirstChannelOnly))
	}
	if provider.extension.SpeechNoiseThreshold != nil {
		query.Set("speech_noise_threshold", strconv.FormatFloat(*provider.extension.SpeechNoiseThreshold, 'f', -1, 64))
	}
	if provider.extension.SentenceMaxLength > 0 {
		query.Set("sentence_max_length", strconv.Itoa(provider.extension.SentenceMaxLength))
	}
	audio, err := input.Audio.Open(requestContext)
	if err != nil {
		return Result{}, newProviderError(AliyunProviderID, "transcribe", ErrorTransient, "audio_open", err)
	}
	defer audio.Close()
	requestURL := provider.endpoint + aliyunFlashPath + "?" + query.Encode()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, requestURL, audio)
	if err != nil {
		return Result{}, newProviderError(AliyunProviderID, "transcribe", ErrorInvalidConfiguration, "request", err)
	}
	request.ContentLength = input.Audio.SizeBytes
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.Canceled) {
			return Result{}, newProviderError(AliyunProviderID, "transcribe", ErrorCanceled, "context", requestContext.Err())
		}
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return Result{}, newProviderError(AliyunProviderID, "transcribe", ErrorTransient, "timeout", requestContext.Err())
		}
		return Result{}, newProviderError(AliyunProviderID, "transcribe", ErrorTransient, "transport", err)
	}
	raw, readErr := readProviderResponse(response.Body)
	if readErr != nil {
		return Result{}, newProviderError(AliyunProviderID, "transcribe", ErrorTransient, "response_read", readErr)
	}
	if response.StatusCode != http.StatusOK {
		return Result{}, httpStatusError(AliyunProviderID, "transcribe", response)
	}
	var envelope aliyunFlashResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Result{}, newProviderError(AliyunProviderID, "transcribe", ErrorRejected, "invalid_json", err)
	}
	if envelope.Status != aliyunFlashSuccessCode {
		return Result{}, classifyAliyunFlashError(envelope.Status)
	}
	result, err := normalizeAliyunFlash(raw, envelope, input.Language, provider.profile.Language)
	if err != nil {
		return Result{}, newProviderError(AliyunProviderID, "transcribe", ErrorRejected, "invalid_response", err)
	}
	result.ProviderID = AliyunProviderID
	result.ProfileID = provider.profile.ID
	return result, nil
}

type aliyunFlashResponse struct {
	TaskID      string `json:"task_id"`
	Status      int    `json:"status"`
	Message     string `json:"message"`
	FlashResult struct {
		Duration  int64                 `json:"duration"`
		Sentences []aliyunFlashSentence `json:"sentences"`
	} `json:"flash_result"`
}

type aliyunFlashSentence struct {
	Text      string            `json:"text"`
	Words     []aliyunFlashWord `json:"words"`
	BeginTime flexibleMillis    `json:"begin_time"`
	EndTime   flexibleMillis    `json:"end_time"`
	ChannelID int               `json:"channel_id"`
}

type aliyunFlashWord struct {
	Text        string         `json:"text"`
	Punctuation string         `json:"punc"`
	BeginTime   flexibleMillis `json:"begin_time"`
	EndTime     flexibleMillis `json:"end_time"`
}

type flexibleMillis int64

func (value *flexibleMillis) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return io.ErrUnexpectedEOF
	}
	if trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return err
		}
		parsed, err := strconv.ParseInt(encoded, 10, 64)
		if err != nil {
			return err
		}
		*value = flexibleMillis(parsed)
		return nil
	}
	parsed, err := strconv.ParseInt(string(trimmed), 10, 64)
	if err != nil {
		return err
	}
	*value = flexibleMillis(parsed)
	return nil
}

func normalizeAliyunFlash(
	raw []byte,
	envelope aliyunFlashResponse,
	requestedLanguage,
	profileLanguage string,
) (Result, error) {
	sentences := append([]aliyunFlashSentence(nil), envelope.FlashResult.Sentences...)
	sort.SliceStable(sentences, func(left, right int) bool {
		if sentences[left].BeginTime != sentences[right].BeginTime {
			return sentences[left].BeginTime < sentences[right].BeginTime
		}
		if sentences[left].EndTime != sentences[right].EndTime {
			return sentences[left].EndTime < sentences[right].EndTime
		}
		return sentences[left].ChannelID < sentences[right].ChannelID
	})
	segments := make([]Segment, 0, len(sentences))
	var text strings.Builder
	for index, sentence := range sentences {
		words := make([]Word, 0, len(sentence.Words))
		for _, source := range sentence.Words {
			words = append(words, Word{
				StartMS: int64(source.BeginTime), EndMS: int64(source.EndTime),
				Text: source.Text + source.Punctuation,
			})
		}
		text.WriteString(sentence.Text)
		segments = append(segments, Segment{
			ID:      fmt.Sprintf("segment-%04d", index+1),
			StartMS: int64(sentence.BeginTime), EndMS: int64(sentence.EndTime),
			Speaker: fmt.Sprintf("channel-%d", sentence.ChannelID),
			Text:    sentence.Text, Words: words,
		})
	}
	language := strings.TrimSpace(requestedLanguage)
	if language == "" {
		language = profileLanguage
	}
	result := Result{
		Language: language, Text: text.String(), Segments: segments,
		RawJSON: append(json.RawMessage(nil), raw...), RawSchema: RawSchemaAliyunFlashV1,
	}
	if err := result.Validate(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func classifyAliyunFlashError(code int) error {
	class := ErrorRejected
	switch code {
	case 40_000_001:
		class = ErrorAuthentication
	case 40_000_005:
		class = ErrorRateLimited
	case 40_000_004, 50_000_000, 50_000_001, 52_010_001:
		class = ErrorTransient
	case 40_000_010:
		class = ErrorAuthorization
	case 40_270_001, 40_270_002, 40_270_003, 40_270_004, 40_270_006:
		class = ErrorInvalidAudio
	}
	return newProviderError(AliyunProviderID, "transcribe", class, strconv.Itoa(code), nil)
}

func decodeAliyunExtension(raw json.RawMessage) (aliyunVendorExtension, error) {
	var extension aliyunVendorExtension
	if err := decodeStrictObject(raw, &extension); err != nil {
		return extension, fmt.Errorf("%w: invalid Aliyun vendor_extension", ErrInvalidProfile)
	}
	if !validOpaqueCredential(extension.AppKey) {
		return extension, fmt.Errorf("%w: appkey is invalid", ErrInvalidProfile)
	}
	for _, value := range []string{extension.VocabularyID, extension.CustomizationID} {
		if value != "" && (!validOpaqueCredential(value) || len(value) > 256) {
			return extension, fmt.Errorf("%w: vendor identifier is invalid", ErrInvalidProfile)
		}
	}
	if extension.SpeechNoiseThreshold != nil &&
		(*extension.SpeechNoiseThreshold < -1 || *extension.SpeechNoiseThreshold > 1) {
		return extension, fmt.Errorf("%w: speech_noise_threshold is outside [-1,1]", ErrInvalidProfile)
	}
	if extension.SentenceMaxLength != 0 &&
		(extension.SentenceMaxLength < 4 || extension.SentenceMaxLength > 50) {
		return extension, fmt.Errorf("%w: sentence_max_length is outside [4,50]", ErrInvalidProfile)
	}
	return extension, nil
}

func aliyunAudioFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "m4a":
		return "mp4"
	default:
		return strings.ToLower(strings.TrimSpace(format))
	}
}

func validateFlashInput(input Input, profile Profile, capabilities Capabilities) error {
	if err := contextErrorForInput(input); err != nil {
		return err
	}
	if input.DurationMS < 0 || input.DurationMS > capabilities.MaxDurationMS {
		return errors.New("audio duration is outside provider limits")
	}
	if input.Audio.SizeBytes < 1 || input.Audio.SizeBytes > capabilities.MaxFileSizeBytes {
		return errors.New("audio size is outside provider limits")
	}
	if !containsFold(capabilities.Formats, input.Audio.Format) ||
		!strings.EqualFold(strings.TrimSpace(input.Audio.Format), strings.TrimSpace(profile.AudioFormat)) {
		return errors.New("audio format does not match the profile")
	}
	return nil
}

func contextErrorForInput(input Input) error {
	if strings.TrimSpace(input.AssetID) == "" || input.Audio == nil || input.Audio.Open == nil {
		return errors.New("asset or audio source is missing")
	}
	return nil
}

func decodeStrictObject(raw json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > maxVendorExtensionBytes || trimmed[0] != '{' {
		return errors.New("value is not a bounded JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("value has trailing JSON")
	}
	return nil
}
