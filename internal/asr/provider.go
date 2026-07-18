// Package asr defines the provider boundary and normalized transcription data.
package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// ErrInconsistentResult indicates that a raw provider response cannot produce
// the normalized fields returned alongside it.
var ErrInconsistentResult = errors.New("ASR raw JSON does not match normalized result")

const (
	RawSchemaNormalizedV1   = "voiceasset.normalized.v1"
	RawSchemaAliyunFlashV1  = "aliyun.flash.v1"
	RawSchemaTencentFlashV1 = "tencent.flash.v1"
)

// Transcriber is the narrow dependency consumed by the durable worker. The
// richer Provider interface below adds management capabilities without making
// the worker depend on profile administration.
type Transcriber interface {
	Transcribe(ctx context.Context, input Input) (Result, error)
}

// Provider is implemented by every managed ASR adapter.
type Provider interface {
	Transcriber
	ID() string
	Capabilities() Capabilities
	ValidateProfile(profile Profile) error
	Health(ctx context.Context) error
	Cancel(ctx context.Context, taskID string) error
}

// Input identifies the source asset and its transcription hints. DurationMS is
// always expressed as an integer number of milliseconds.
type Input struct {
	AssetID    string    `json:"asset_id"`
	Language   string    `json:"language"`
	DurationMS int64     `json:"duration_ms"`
	Audio      *Audio    `json:"-"`
	Hotwords   []Hotword `json:"hotwords,omitempty"`
}

// Audio is a bounded, reopenable source passed to synchronous providers. Open
// must return an independent stream on every call so retries and failover never
// reuse a partially consumed reader. The provider closes every opened stream.
type Audio struct {
	Open       func(context.Context) (io.ReadCloser, error) `json:"-"`
	SizeBytes  int64                                        `json:"size_bytes"`
	Format     string                                       `json:"format"`
	SampleRate int                                          `json:"sample_rate"`
}

// Hotword is the provider-neutral representation compiled by each adapter.
// Weight uses the product's semantic 1-100 scale; adapters must clamp or map it
// only according to documented vendor limits.
type Hotword struct {
	Term   string `json:"term"`
	Weight int    `json:"weight"`
}

// Result preserves both normalized transcript data and the immutable bytes
// received from the provider. RawJSON is intentionally excluded when encoding
// Result so it cannot be mistaken for part of the normalized transcript.
type Result struct {
	Language   string          `json:"language"`
	Text       string          `json:"text"`
	Segments   []Segment       `json:"segments"`
	RawJSON    json.RawMessage `json:"-"`
	RawSchema  string          `json:"-"`
	ProviderID string          `json:"-"`
	ProfileID  string          `json:"-"`
}

// Segment is a timestamped portion of a normalized transcript.
type Segment struct {
	ID         string   `json:"id"`
	StartMS    int64    `json:"start_ms"`
	EndMS      int64    `json:"end_ms"`
	Speaker    string   `json:"speaker"`
	Text       string   `json:"text"`
	Confidence *float64 `json:"confidence"`
	Words      []Word   `json:"words"`
}

// Word is the smallest timestamped transcription unit retained by the
// normalized model.
type Word struct {
	StartMS    int64    `json:"start_ms"`
	EndMS      int64    `json:"end_ms"`
	Text       string   `json:"text"`
	Confidence *float64 `json:"confidence"`
}

// Validate proves that the normalized result and immutable vendor envelope are
// structurally valid. Provider contract tests separately prove each vendor
// mapping because vendor envelopes intentionally differ from this model.
func (result Result) Validate() error {
	return ValidateResult(result)
}

// ValidateResult rejects malformed normalized timelines and non-object JSON.
func ValidateResult(result Result) error {
	trimmed := bytes.TrimSpace(result.RawJSON)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return fmt.Errorf("%w: raw response is not one JSON object", ErrInconsistentResult)
	}
	if err := validateNormalized(normalizedFromResult(result)); err != nil {
		return fmt.Errorf("%w: %v", ErrInconsistentResult, err)
	}
	switch result.RawSchema {
	case "", RawSchemaNormalizedV1:
		raw, err := decodeNormalized(result.RawJSON)
		if err != nil {
			return fmt.Errorf("%w: decode normalized raw JSON: %v", ErrInconsistentResult, err)
		}
		if !reflect.DeepEqual(raw, normalizedFromResult(result)) {
			return ErrInconsistentResult
		}
	case RawSchemaAliyunFlashV1, RawSchemaTencentFlashV1:
		// Provider fixture tests own the vendor-specific mapping proof.
	default:
		return fmt.Errorf("%w: unsupported raw response schema", ErrInconsistentResult)
	}
	return nil
}

type normalizedResult struct {
	Language string    `json:"language"`
	Text     string    `json:"text"`
	Segments []Segment `json:"segments"`
}

func resultFromRaw(rawJSON string) (Result, error) {
	rawBytes := json.RawMessage([]byte(rawJSON))
	normalized, err := decodeNormalized(rawBytes)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Language:  normalized.Language,
		Text:      normalized.Text,
		Segments:  normalized.Segments,
		RawJSON:   rawBytes,
		RawSchema: RawSchemaNormalizedV1,
	}, nil
}

func decodeNormalized(rawJSON []byte) (normalizedResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(rawJSON))
	decoder.DisallowUnknownFields()
	var result normalizedResult
	if err := decoder.Decode(&result); err != nil {
		return normalizedResult{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return normalizedResult{}, errors.New("raw JSON contains multiple values")
		}
		return normalizedResult{}, fmt.Errorf("decode trailing raw JSON: %w", err)
	}
	if err := validateNormalized(result); err != nil {
		return normalizedResult{}, err
	}
	return result, nil
}

func validateNormalized(result normalizedResult) error {
	if result.Language == "" {
		return errors.New("language is empty")
	}
	if result.Text == "" {
		return errors.New("text is empty")
	}
	if len(result.Segments) == 0 {
		return errors.New("segments are empty")
	}
	for segmentIndex, segment := range result.Segments {
		if strings.TrimSpace(segment.ID) == "" {
			return fmt.Errorf("segment %d has an empty ID", segmentIndex)
		}
		if segment.StartMS < 0 || segment.EndMS < segment.StartMS {
			return fmt.Errorf("segment %q has an invalid timeline", segment.ID)
		}
		if strings.TrimSpace(segment.Text) == "" {
			return fmt.Errorf("segment %q has empty text", segment.ID)
		}
		if segment.Confidence != nil && (*segment.Confidence < 0 || *segment.Confidence > 1) {
			return fmt.Errorf("segment %q has invalid confidence", segment.ID)
		}
		for wordIndex, word := range segment.Words {
			if word.Text == "" {
				return fmt.Errorf("segment %q word %d has empty text", segment.ID, wordIndex)
			}
			if word.StartMS < segment.StartMS || word.EndMS < word.StartMS || word.EndMS > segment.EndMS {
				return fmt.Errorf("segment %q word %d has an invalid timeline", segment.ID, wordIndex)
			}
			if word.Confidence != nil && (*word.Confidence < 0 || *word.Confidence > 1) {
				return fmt.Errorf("segment %q word %d has invalid confidence", segment.ID, wordIndex)
			}
		}
	}
	return nil
}

func normalizedFromResult(result Result) normalizedResult {
	return normalizedResult{
		Language: result.Language,
		Text:     result.Text,
		Segments: result.Segments,
	}
}
