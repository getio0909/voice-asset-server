package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestMockProviderReturnsLanguageFixture(t *testing.T) {
	tests := []struct {
		name         string
		language     string
		wantLanguage string
		wantText     string
	}{
		{
			name:         "Chinese language tag",
			language:     "zh-CN",
			wantLanguage: "zh-CN",
			wantText:     "欢迎使用语音资产。",
		},
		{
			name:         "Chinese base language is case insensitive",
			language:     " ZH ",
			wantLanguage: "zh-CN",
			wantText:     "欢迎使用语音资产。",
		},
		{
			name:         "other language",
			language:     "fr-FR",
			wantLanguage: "en-US",
			wantText:     "Welcome to VoiceAsset.",
		},
	}

	provider := NewMockProvider()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := provider.Transcribe(context.Background(), Input{
				AssetID:    "asset-001",
				Language:   test.language,
				DurationMS: 4_000,
			})
			if err != nil {
				t.Fatalf("Transcribe() error = %v", err)
			}
			if result.Language != test.wantLanguage || result.Text != test.wantText {
				t.Fatalf("Transcribe() = language %q, text %q", result.Language, result.Text)
			}
			if len(result.Segments) != 2 {
				t.Fatalf("Transcribe() segment count = %d, want 2", len(result.Segments))
			}
			assertIntegerMillisecondTimeline(t, result)
			if err := result.Validate(); err != nil {
				t.Fatalf("Result.Validate() error = %v", err)
			}
			normalizedJSON, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("marshal normalized result: %v", err)
			}
			if !bytes.Equal(normalizedJSON, result.RawJSON) {
				t.Fatalf("normalized JSON and raw JSON differ:\nnormalized: %s\nraw:        %s", normalizedJSON, result.RawJSON)
			}
		})
	}
}

func TestMockProviderReplayIsByteIdenticalAndIsolated(t *testing.T) {
	provider := NewMockProvider()
	input := Input{AssetID: "asset-001", Language: "en-US", DurationMS: 4_000}

	first, err := provider.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("first Transcribe() error = %v", err)
	}
	second, err := provider.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("second Transcribe() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replayed result differs:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if !bytes.Equal(first.RawJSON, second.RawJSON) {
		t.Fatal("replayed raw JSON differs")
	}

	first.Text = "changed"
	first.Segments[0].Text = "changed"
	first.Segments[0].Words[0].Text = "changed"
	first.RawJSON[0] = '['

	third, err := provider.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("third Transcribe() error = %v", err)
	}
	if !reflect.DeepEqual(second, third) {
		t.Fatalf("caller mutation polluted replay:\nsecond: %+v\nthird:  %+v", second, third)
	}
	if !bytes.Equal(second.RawJSON, third.RawJSON) {
		t.Fatal("caller mutation polluted replay raw JSON")
	}
}

func TestMockProviderFixtureDependsOnlyOnLanguageClass(t *testing.T) {
	provider := NewMockProvider()
	first, err := provider.Transcribe(context.Background(), Input{
		AssetID:    "asset-one",
		Language:   "en-US",
		DurationMS: 1_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Transcribe(context.Background(), Input{
		AssetID:    "asset-two",
		Language:   "de-DE",
		DurationMS: 99_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("non-Chinese inputs did not replay the same fixed English fixture")
	}
}

func TestMockProviderHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := NewMockProvider().Transcribe(ctx, Input{Language: "zh-CN"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Transcribe() error = %v, want context.Canceled", err)
	}
	if !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("Transcribe() result = %+v, want zero result", result)
	}
}

func TestValidateResultDetectsNormalizedAndRawMismatch(t *testing.T) {
	result, err := NewMockProvider().Transcribe(context.Background(), Input{Language: "en-US"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*Result)
	}{
		{
			name: "normalized text",
			mutate: func(result *Result) {
				result.Text = "different"
			},
		},
		{
			name: "raw text",
			mutate: func(result *Result) {
				var raw map[string]any
				if err := json.Unmarshal(result.RawJSON, &raw); err != nil {
					t.Fatal(err)
				}
				raw["text"] = "different"
				result.RawJSON, err = json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "fractional raw timestamp",
			mutate: func(result *Result) {
				result.RawJSON = bytes.Replace(result.RawJSON, []byte(`"start_ms":0`), []byte(`"start_ms":0.5`), 1)
			},
		},
		{
			name: "invalid raw JSON",
			mutate: func(result *Result) {
				result.RawJSON = json.RawMessage(`{"language":`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneResult(result)
			test.mutate(&changed)
			if err := changed.Validate(); !errors.Is(err, ErrInconsistentResult) {
				t.Fatalf("Result.Validate() error = %v, want ErrInconsistentResult", err)
			}
		})
	}
}

func assertIntegerMillisecondTimeline(t *testing.T, result Result) {
	t.Helper()
	for _, segment := range result.Segments {
		if segment.ID == "" || segment.StartMS < 0 || segment.EndMS < segment.StartMS {
			t.Fatalf("invalid segment timeline: %+v", segment)
		}
		for _, word := range segment.Words {
			if word.StartMS < segment.StartMS || word.EndMS < word.StartMS || word.EndMS > segment.EndMS {
				t.Fatalf("invalid word timeline in segment %q: %+v", segment.ID, word)
			}
		}
	}
}

func cloneResult(result Result) Result {
	cloned := result
	cloned.RawJSON = append(json.RawMessage(nil), result.RawJSON...)
	cloned.Segments = append([]Segment(nil), result.Segments...)
	for index := range cloned.Segments {
		cloned.Segments[index].Words = append([]Word(nil), result.Segments[index].Words...)
	}
	return cloned
}
