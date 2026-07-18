package asr_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
)

// TestLiveProviders is deliberately opt-in. Missing vendor credentials skip
// only that provider so fixture tests and release candidates remain offline.
// Error output is limited to the provider-neutral class and ProviderError's
// sanitized code. It never includes a credential, signed URL, vendor message,
// or raw vendor response.
func TestLiveProviders(t *testing.T) {
	if os.Getenv("VOICEASSET_LIVE_ASR") != "1" {
		t.Skip("set VOICEASSET_LIVE_ASR=1 to run vendor ASR tests")
	}
	audio := liveAudioFixture(t)
	t.Run("TencentFlash", func(t *testing.T) {
		appID := firstEnvironment("TENCENT_ASR_APP_ID", "TENCENT_ASR_USER_ID")
		secretID := os.Getenv("TENCENT_ASR_SECRETID")
		secretKey := os.Getenv("TENCENT_ASR_SECRETKEY")
		if appID == "" || secretID == "" || secretKey == "" {
			t.Skip("Tencent AppID, SecretID, and SecretKey are not all configured")
		}
		profile := asr.DefaultTencentFlashProfile(appID)
		profile.ID = "91000000-0000-4000-8000-000000000001"
		profile.AudioFormat = audio.Format
		profile.Language = audio.Language
		profile.Model = tencentModel(audio.Language)
		provider, err := asr.NewTencentFlashProvider(profile, asr.TencentCredentials{
			SecretID: secretID, SecretKey: secretKey,
		}, nil)
		if err != nil {
			t.Fatalf("configure Tencent live provider: class=%s", asr.ErrorClassOf(err))
		}
		input := asr.Input{
			AssetID:  "92000000-0000-4000-8000-000000000001",
			Language: audio.Language, DurationMS: audio.DurationMS, Audio: audio.Audio,
		}
		if os.Getenv("VOICEASSET_LIVE_ASR_HOTWORDS") == "1" {
			input.Hotwords = []asr.Hotword{{Term: "VoiceAsset", Weight: 90}}
		}
		result, err := provider.Transcribe(liveContext(t), input)
		assertLiveResult(t, result, err, asr.TencentProviderID)
	})

	t.Run("AliyunFlash", func(t *testing.T) {
		appKey := firstEnvironment("ALIYUN_ASR_APP_KEY", "ALIYUN_ASR_KEY")
		accessToken := os.Getenv("ALIYUN_ASR_ACCESS_TOKEN")
		accessKeyID := os.Getenv("ALIYUN_ACCESS_KEY_ID")
		accessKeySecret := os.Getenv("ALIYUN_ACCESS_KEY_SECRET")
		hasToken := accessToken != "" && accessKeyID == "" && accessKeySecret == ""
		hasAccessKey := accessToken == "" && accessKeyID != "" && accessKeySecret != ""
		if appKey == "" || (!hasToken && !hasAccessKey) {
			t.Skip("Aliyun AppKey plus either NLS token or AccessKey pair are not configured")
		}
		extension, err := json.Marshal(struct {
			AppKey string `json:"appkey"`
		}{AppKey: appKey})
		if err != nil {
			t.Fatal("encode Aliyun live profile")
		}
		profile := asr.DefaultAliyunFlashProfile()
		profile.ID = "91000000-0000-4000-8000-000000000002"
		profile.AudioFormat = audio.Format
		profile.Language = audio.Language
		profile.VendorExtension = extension
		provider, err := asr.NewAliyunFlashProvider(profile, asr.AliyunCredentials{
			AccessKeyID: accessKeyID, AccessKeySecret: accessKeySecret, AccessToken: accessToken,
		}, nil)
		if err != nil {
			t.Fatalf("configure Aliyun live provider: class=%s", asr.ErrorClassOf(err))
		}
		result, err := provider.Transcribe(liveContext(t), asr.Input{
			AssetID:  "92000000-0000-4000-8000-000000000002",
			Language: audio.Language, DurationMS: audio.DurationMS, Audio: audio.Audio,
		})
		assertLiveResult(t, result, err, asr.AliyunProviderID)
	})
}

type liveFixture struct {
	*asr.Audio
	Language   string
	DurationMS int64
}

func liveAudioFixture(t *testing.T) liveFixture {
	t.Helper()
	path := strings.TrimSpace(os.Getenv("VOICEASSET_LIVE_ASR_AUDIO"))
	if path == "" {
		t.Fatal("VOICEASSET_LIVE_ASR_AUDIO must name a local audio fixture")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 {
		t.Fatal("live ASR audio fixture is unavailable")
	}
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	if format == "mp4" {
		format = "m4a"
	}
	language := strings.TrimSpace(os.Getenv("VOICEASSET_LIVE_ASR_LANGUAGE"))
	if language == "" {
		language = "en-US"
	}
	duration := int64(10_000)
	return liveFixture{
		Audio: &asr.Audio{
			SizeBytes: info.Size(), Format: format, SampleRate: 16_000,
			Open: func(context.Context) (io.ReadCloser, error) { return os.Open(path) },
		},
		Language: language, DurationMS: duration,
	}
}

func liveContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func assertLiveResult(t *testing.T, result asr.Result, err error, providerID string) {
	t.Helper()
	if err != nil {
		var providerError *asr.ProviderError
		if errors.As(err, &providerError) {
			t.Fatalf(
				"live transcription failed: provider=%s class=%s detail=%s",
				providerID, asr.ErrorClassOf(err), providerError,
			)
		}
		t.Fatalf("live transcription failed: provider=%s class=%s detail=unknown", providerID, asr.ErrorClassOf(err))
	}
	if result.ProviderID != providerID || strings.TrimSpace(result.Text) == "" ||
		len(result.Segments) == 0 || result.Validate() != nil {
		t.Fatalf("live transcription returned an invalid normalized result: provider=%s", providerID)
	}
}

func firstEnvironment(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func tencentModel(language string) string {
	if strings.HasPrefix(strings.ToLower(language), "en") {
		return "16k_en"
	}
	return "16k_zh"
}
