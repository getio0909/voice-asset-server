package asr

import (
	"context"
	"crypto/hmac"
	"crypto/sha1" // #nosec G505 -- required by Tencent Flash ASR's documented signature protocol.
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	TencentProviderID    = "tencent_asr"
	tencentFlashEndpoint = "https://asr.cloud.tencent.com"
	tencentFlashHost     = "asr.cloud.tencent.com"
)

// TencentCredentials contains the two values used by the documented Flash ASR
// signature. They are never serialized or included in error messages.
type TencentCredentials struct {
	SecretID  string `json:"-"`
	SecretKey string `json:"-"`
}

func (TencentCredentials) String() string { return "TencentCredentials{REDACTED}" }

type tencentVendorExtension struct {
	AppID             string `json:"appid"`
	HotwordID         string `json:"hotword_id,omitempty"`
	CustomizationID   string `json:"customization_id,omitempty"`
	FirstChannelOnly  *bool  `json:"first_channel_only,omitempty"`
	FilterDirty       int    `json:"filter_dirty,omitempty"`
	FilterModal       int    `json:"filter_modal,omitempty"`
	SentenceMaxLength int    `json:"sentence_max_length,omitempty"`
}

// TencentFlashProvider implements the documented synchronous Flash ASR API.
type TencentFlashProvider struct {
	profile     Profile
	extension   tencentVendorExtension
	credentials TencentCredentials
	client      *http.Client
	endpoint    string
	now         func() time.Time
}

var _ Provider = (*TencentFlashProvider)(nil)

func DefaultTencentFlashProfile(appID string) Profile {
	extension, _ := json.Marshal(tencentVendorExtension{AppID: appID})
	return Profile{
		ProviderID: TencentProviderID, Model: "16k_zh", Language: "zh-CN",
		SampleRate: 16_000, AudioFormat: "m4a",
		Punctuation: true, Timestamps: true, WordTimestamps: true,
		NumberNormalization: true, Timeout: 2 * time.Minute,
		Retry:       RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 30 * time.Second},
		Concurrency: 20, VendorExtension: extension,
	}
}

func NewTencentFlashProvider(
	profile Profile,
	credentials TencentCredentials,
	client *http.Client,
) (*TencentFlashProvider, error) {
	provider := &TencentFlashProvider{
		profile: profile, credentials: credentials,
		client: providerHTTPClient(client), endpoint: tencentFlashEndpoint, now: time.Now,
	}
	if err := provider.ValidateProfile(profile); err != nil {
		return nil, err
	}
	if !validOpaqueCredential(credentials.SecretID) || !validOpaqueCredential(credentials.SecretKey) {
		return nil, newProviderError(
			TencentProviderID, "configure", ErrorInvalidConfiguration, "invalid_credentials", nil,
		)
	}
	extension, err := decodeTencentExtension(profile.VendorExtension)
	if err != nil {
		return nil, err
	}
	provider.extension = extension
	return provider, nil
}

func (*TencentFlashProvider) ID() string { return TencentProviderID }

func (*TencentFlashProvider) Capabilities() Capabilities {
	return Capabilities{
		ProviderID: TencentProviderID, Batch: true,
		Languages: []string{"*"},
		Models: []string{
			"8k_zh", "8k_en", "8k_zh_large", "16k_zh_en", "16k_multi_lang",
			"16k_zh", "16k_zh-PY", "16k_yue", "16k_en", "16k_ja", "16k_ko",
			"16k_vi", "16k_ms", "16k_id", "16k_fil", "16k_th", "16k_pt",
			"16k_tr", "16k_ar", "16k_es", "16k_hi", "16k_fr", "16k_de",
		},
		Formats:     []string{"wav", "pcm", "ogg-opus", "speex", "silk", "mp3", "m4a", "aac", "amr"},
		SampleRates: []int{8_000, 16_000}, Hotwords: true, TemporaryHotwords: true,
		Timestamps: true, WordTimestamps: true, SpeakerDiarization: true,
		Punctuation: true, NumberNormalization: true,
		MaxDurationMS: 2 * 60 * 60 * 1_000, MaxFileSizeBytes: maxFlashAudioBytes,
		MaxConcurrency: 20,
	}
}

func (provider *TencentFlashProvider) ValidateProfile(profile Profile) error {
	if err := ValidateProfileAgainst(profile, provider.Capabilities()); err != nil {
		return err
	}
	endpoint := strings.TrimSuffix(strings.TrimSpace(profile.Endpoint), "/")
	if endpoint != "" && endpoint != tencentFlashEndpoint {
		return fmt.Errorf("%w: endpoint is not the official Tencent gateway", ErrInvalidProfile)
	}
	if strings.TrimSpace(profile.Region) != "" {
		return fmt.Errorf("%w: Flash ASR does not accept a region override", ErrInvalidProfile)
	}
	if strings.TrimSpace(profile.Language) == "" {
		return fmt.Errorf("%w: language must not be empty", ErrInvalidProfile)
	}
	if strings.HasPrefix(strings.ToLower(profile.Model), "8k_") && profile.SampleRate != 8_000 ||
		strings.HasPrefix(strings.ToLower(profile.Model), "16k_") && profile.SampleRate != 16_000 {
		return fmt.Errorf("%w: sample_rate does not match engine_type", ErrInvalidProfile)
	}
	if profile.SpeakerDiarization && profile.Model != "8k_zh" && profile.Model != "16k_zh" {
		return fmt.Errorf("%w: speaker diarization requires a Mandarin engine", ErrInvalidProfile)
	}
	if tencentLargeModel(profile.Model) && profile.Concurrency > 5 {
		return fmt.Errorf("%w: large-model concurrency exceeds the documented free limit", ErrInvalidProfile)
	}
	_, err := decodeTencentExtension(profile.VendorExtension)
	return err
}

func (provider *TencentFlashProvider) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, provider.endpoint, nil)
	if err != nil {
		return newProviderError(TencentProviderID, "health", ErrorInvalidConfiguration, "request", err)
	}
	response, err := provider.client.Do(request)
	if err != nil {
		return newProviderError(TencentProviderID, "health", ErrorTransient, "transport", err)
	}
	_ = response.Body.Close()
	if response.StatusCode >= 500 {
		return httpStatusError(TencentProviderID, "health", response)
	}
	return nil
}

func (*TencentFlashProvider) Cancel(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return newProviderError(
		TencentProviderID, "cancel", ErrorUnsupported, "synchronous", ErrUnsupportedOperation,
	)
}

func (provider *TencentFlashProvider) Transcribe(ctx context.Context, input Input) (Result, error) {
	if err := validateFlashInput(input, provider.profile, provider.Capabilities()); err != nil {
		return Result{}, newProviderError(TencentProviderID, "transcribe", ErrorInvalidAudio, "invalid_input", err)
	}
	temporaryHotwords, err := compileTencentHotwords(input.Hotwords)
	if err != nil {
		return Result{}, newProviderError(TencentProviderID, "transcribe", ErrorInvalidConfiguration, "hotwords", err)
	}
	requestContext, cancel := context.WithTimeout(ctx, provider.profile.Timeout)
	defer cancel()

	query := url.Values{}
	query.Set("secretid", provider.credentials.SecretID)
	query.Set("engine_type", provider.profile.Model)
	query.Set("voice_format", strings.ToLower(strings.TrimSpace(input.Audio.Format)))
	query.Set("timestamp", strconv.FormatInt(provider.now().UTC().Unix(), 10))
	query.Set("speaker_diarization", boolInt(provider.profile.SpeakerDiarization))
	query.Set("filter_dirty", strconv.Itoa(provider.extension.FilterDirty))
	query.Set("filter_modal", strconv.Itoa(provider.extension.FilterModal))
	if provider.profile.Punctuation {
		query.Set("filter_punc", "0")
	} else {
		query.Set("filter_punc", "2")
	}
	query.Set("convert_num_mode", boolInt(provider.profile.NumberNormalization))
	if provider.profile.WordTimestamps {
		query.Set("word_info", "2")
	} else {
		query.Set("word_info", "0")
	}
	firstChannelOnly := true
	if provider.extension.FirstChannelOnly != nil {
		firstChannelOnly = *provider.extension.FirstChannelOnly
	}
	query.Set("first_channel_only", boolInt(firstChannelOnly))
	if provider.extension.HotwordID != "" {
		query.Set("hotword_id", provider.extension.HotwordID)
	}
	if provider.extension.CustomizationID != "" {
		query.Set("customization_id", provider.extension.CustomizationID)
	}
	if provider.extension.SentenceMaxLength > 0 {
		query.Set("sentence_max_length", strconv.Itoa(provider.extension.SentenceMaxLength))
	}
	if temporaryHotwords != "" {
		query.Set("hotword_list", temporaryHotwords)
	}

	path := "/asr/flash/v1/" + provider.extension.AppID
	rawQuery := query.Encode()
	signature := tencentFlashSignature(tencentFlashHost, path, rawQuery, provider.credentials.SecretKey)
	audio, err := input.Audio.Open(requestContext)
	if err != nil {
		return Result{}, newProviderError(TencentProviderID, "transcribe", ErrorTransient, "audio_open", err)
	}
	defer audio.Close()
	request, err := http.NewRequestWithContext(
		requestContext, http.MethodPost, provider.endpoint+path+"?"+rawQuery, audio,
	)
	if err != nil {
		return Result{}, newProviderError(TencentProviderID, "transcribe", ErrorInvalidConfiguration, "request", err)
	}
	request.ContentLength = input.Audio.SizeBytes
	request.Header.Set("Authorization", signature)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		if errors.Is(requestContext.Err(), context.Canceled) {
			return Result{}, newProviderError(TencentProviderID, "transcribe", ErrorCanceled, "context", requestContext.Err())
		}
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return Result{}, newProviderError(TencentProviderID, "transcribe", ErrorTransient, "timeout", requestContext.Err())
		}
		return Result{}, newProviderError(TencentProviderID, "transcribe", ErrorTransient, "transport", err)
	}
	raw, readErr := readProviderResponse(response.Body)
	if readErr != nil {
		return Result{}, newProviderError(TencentProviderID, "transcribe", ErrorTransient, "response_read", readErr)
	}
	if response.StatusCode != http.StatusOK {
		return Result{}, httpStatusError(TencentProviderID, "transcribe", response)
	}
	var envelope tencentFlashResponse
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Result{}, newProviderError(TencentProviderID, "transcribe", ErrorRejected, "invalid_json", err)
	}
	if envelope.Code != 0 {
		return Result{}, classifyTencentFlashError(envelope.Code)
	}
	result, err := normalizeTencentFlash(raw, envelope, input.Language, provider.profile.Language, provider.profile.SpeakerDiarization)
	if err != nil {
		return Result{}, newProviderError(
			TencentProviderID, "transcribe", ErrorRejected,
			tencentNormalizationErrorCode(envelope), err,
		)
	}
	result.ProviderID = TencentProviderID
	result.ProfileID = provider.profile.ID
	return result, nil
}

func tencentNormalizationErrorCode(envelope tencentFlashResponse) string {
	if len(envelope.FlashResult) == 0 {
		return "empty_result"
	}
	sentenceCount := 0
	hasChannelText := false
	for _, channel := range envelope.FlashResult {
		sentenceCount += len(channel.SentenceList)
		hasChannelText = hasChannelText || strings.TrimSpace(channel.Text) != ""
	}
	if sentenceCount == 0 {
		if hasChannelText {
			return "missing_sentences"
		}
		return "empty_result"
	}
	for _, channel := range envelope.FlashResult {
		for _, sentence := range channel.SentenceList {
			if strings.TrimSpace(sentence.Text) == "" {
				return "empty_sentence"
			}
			if sentence.StartTime < 0 || sentence.EndTime < sentence.StartTime {
				return "invalid_timeline"
			}
			for _, word := range sentence.WordList {
				if word.Word == "" {
					return "empty_word"
				}
				if word.EndTime < word.StartTime {
					return "invalid_word_timeline"
				}
			}
		}
	}
	return "invalid_response"
}

type tencentFlashResponse struct {
	RequestID     string                 `json:"request_id"`
	Code          int                    `json:"code"`
	Message       string                 `json:"message"`
	AudioDuration int64                  `json:"audio_duration"`
	FlashResult   []tencentChannelResult `json:"flash_result"`
}

type tencentChannelResult struct {
	Text         string                  `json:"text"`
	ChannelID    int                     `json:"channel_id"`
	SentenceList []tencentSentenceResult `json:"sentence_list"`
}

type tencentSentenceResult struct {
	Text      string              `json:"text"`
	StartTime int64               `json:"start_time"`
	EndTime   int64               `json:"end_time"`
	SpeakerID int                 `json:"speaker_id"`
	WordList  []tencentWordResult `json:"word_list"`
	channelID int
}

type tencentWordResult struct {
	Word      string `json:"word"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
}

func normalizeTencentFlash(
	raw []byte,
	envelope tencentFlashResponse,
	requestedLanguage,
	profileLanguage string,
	diarization bool,
) (Result, error) {
	sentences := make([]tencentSentenceResult, 0)
	for _, channel := range envelope.FlashResult {
		for _, sentence := range channel.SentenceList {
			sentence.channelID = channel.ChannelID
			sentences = append(sentences, sentence)
		}
	}
	sort.SliceStable(sentences, func(left, right int) bool {
		if sentences[left].StartTime != sentences[right].StartTime {
			return sentences[left].StartTime < sentences[right].StartTime
		}
		if sentences[left].EndTime != sentences[right].EndTime {
			return sentences[left].EndTime < sentences[right].EndTime
		}
		return sentences[left].channelID < sentences[right].channelID
	})
	segments := make([]Segment, 0, len(sentences))
	var text strings.Builder
	for index, sentence := range sentences {
		words := make([]Word, 0, len(sentence.WordList))
		segmentStart := sentence.StartTime
		segmentEnd := sentence.EndTime
		for _, source := range sentence.WordList {
			if source.StartTime < segmentStart {
				segmentStart = source.StartTime
			}
			if source.EndTime > segmentEnd {
				segmentEnd = source.EndTime
			}
			words = append(words, Word{StartMS: source.StartTime, EndMS: source.EndTime, Text: source.Word})
		}
		speaker := fmt.Sprintf("channel-%d", sentence.channelID)
		if diarization {
			speaker = fmt.Sprintf("speaker-%d", sentence.SpeakerID)
		}
		text.WriteString(sentence.Text)
		segments = append(segments, Segment{
			ID:      fmt.Sprintf("segment-%04d", index+1),
			StartMS: segmentStart, EndMS: segmentEnd,
			Speaker: speaker, Text: sentence.Text, Words: words,
		})
	}
	language := strings.TrimSpace(requestedLanguage)
	if language == "" {
		language = profileLanguage
	}
	result := Result{
		Language: language, Text: text.String(), Segments: segments,
		RawJSON: append(json.RawMessage(nil), raw...), RawSchema: RawSchemaTencentFlashV1,
	}
	if err := result.Validate(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func tencentFlashSignature(host, path, rawQuery, secret string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	_, _ = mac.Write([]byte("POST" + host + path + "?" + rawQuery))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func classifyTencentFlashError(code int) error {
	class := ErrorRejected
	switch code {
	case 4002:
		class = ErrorAuthentication
	case 4003, 4004, 4005:
		class = ErrorAuthorization
	case 4006:
		class = ErrorRateLimited
	case 4007, 4011, 4012:
		class = ErrorInvalidAudio
	case 4008, 4009, 5001, 5002, 5003:
		class = ErrorTransient
	}
	return newProviderError(TencentProviderID, "transcribe", class, strconv.Itoa(code), nil)
}

func compileTencentHotwords(hotwords []Hotword) (string, error) {
	if len(hotwords) > 128 {
		return "", errors.New("Tencent temporary hotwords exceed 128 entries")
	}
	seen := make(map[string]struct{}, len(hotwords))
	compiled := make([]string, 0, len(hotwords))
	for _, hotword := range hotwords {
		term := strings.TrimSpace(hotword.Term)
		if !utf8.ValidString(term) || term == "" || utf8.RuneCountInString(term) > 30 ||
			strings.ContainsAny(term, ",|\r\n") {
			return "", errors.New("Tencent temporary hotword term is invalid")
		}
		for _, character := range term {
			if unicode.IsControl(character) {
				return "", errors.New("Tencent temporary hotword contains a control character")
			}
		}
		key := strings.ToLower(term)
		if _, exists := seen[key]; exists {
			return "", errors.New("Tencent temporary hotwords contain a duplicate term")
		}
		seen[key] = struct{}{}
		if hotword.Weight < 1 || hotword.Weight > 100 {
			return "", errors.New("Tencent temporary hotword weight is outside [1,100]")
		}
		// The neutral 1-100 scale maps to Tencent's ordinary 1-10 range.
		// Super-hotword 11 and forced-homophone 100 require an explicit future
		// provider mapping and are never selected implicitly.
		vendorWeight := (hotword.Weight + 9) / 10
		compiled = append(compiled, term+"|"+strconv.Itoa(vendorWeight))
	}
	return strings.Join(compiled, ","), nil
}

func decodeTencentExtension(raw json.RawMessage) (tencentVendorExtension, error) {
	var extension tencentVendorExtension
	if err := decodeStrictObject(raw, &extension); err != nil {
		return extension, fmt.Errorf("%w: invalid Tencent vendor_extension", ErrInvalidProfile)
	}
	if len(extension.AppID) < 5 || len(extension.AppID) > 20 {
		return extension, fmt.Errorf("%w: appid is invalid", ErrInvalidProfile)
	}
	if _, err := strconv.ParseUint(extension.AppID, 10, 64); err != nil {
		return extension, fmt.Errorf("%w: appid is invalid", ErrInvalidProfile)
	}
	for _, value := range []string{extension.HotwordID, extension.CustomizationID} {
		if value != "" && (!validOpaqueCredential(value) || len(value) > 256) {
			return extension, fmt.Errorf("%w: vendor identifier is invalid", ErrInvalidProfile)
		}
	}
	if extension.FilterDirty < 0 || extension.FilterDirty > 2 ||
		extension.FilterModal < 0 || extension.FilterModal > 2 {
		return extension, fmt.Errorf("%w: filter mode is outside [0,2]", ErrInvalidProfile)
	}
	if extension.SentenceMaxLength != 0 &&
		(extension.SentenceMaxLength < 6 || extension.SentenceMaxLength > 40) {
		return extension, fmt.Errorf("%w: sentence_max_length is outside [6,40]", ErrInvalidProfile)
	}
	return extension, nil
}

func tencentLargeModel(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "8k_zh_large", "16k_zh_en", "16k_multi_lang":
		return true
	default:
		return false
	}
}

func boolInt(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
