// Package realtime owns the provider-neutral realtime transcription protocol
// and its durable session state machine.
package realtime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const (
	ProtocolVersion       = "1"
	EncodingPCMS16LE      = "pcm_s16le"
	MaxFrameBytes         = 64 * 1024
	MaxClientMessageBytes = 96 * 1024
	MaxServerMessageBytes = 320 * 1024
	MaxTranscriptBytes    = 256 * 1024
)

const (
	EventStart     = "start"
	EventResume    = "resume"
	EventAudio     = "audio"
	EventFinish    = "finish"
	EventHeartbeat = "heartbeat"
)

var (
	ErrInvalidEvent    = errors.New("invalid realtime event")
	canonicalSHA256    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	languageTagPattern = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`)
)

type StartEvent struct {
	Type              string `json:"type"`
	ProtocolVersion   string `json:"protocol_version"`
	ClientSessionID   string `json:"client_session_id"`
	IdempotencyKey    string `json:"idempotency_key"`
	Encoding          string `json:"encoding"`
	SampleRateHz      int    `json:"sample_rate_hz"`
	Channels          int    `json:"channels"`
	FrameDurationMS   int    `json:"frame_duration_ms"`
	Language          string `json:"language"`
	ProviderProfileID string `json:"provider_profile_id,omitempty"`
	HotwordSetID      string `json:"hotword_set_id,omitempty"`
}

type ResumeEvent struct {
	Type                     string `json:"type"`
	ProtocolVersion          string `json:"protocol_version"`
	SessionID                string `json:"session_id"`
	LastAcknowledgedSequence int64  `json:"last_acknowledged_sequence"`
}

type AudioEvent struct {
	Type         string `json:"type"`
	SessionID    string `json:"session_id"`
	Sequence     int64  `json:"sequence"`
	CapturedAtMS int64  `json:"captured_at_ms"`
	PCMBase64    string `json:"pcm_base64"`
}

type FinishEvent struct {
	Type                string `json:"type"`
	SessionID           string `json:"session_id"`
	FinalSequence       int64  `json:"final_sequence"`
	CapturedDurationMS  int64  `json:"captured_duration_ms"`
	ClientArchiveSHA256 string `json:"client_archive_sha256"`
}

type HeartbeatEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	SentAtMS  int64  `json:"sent_at_ms"`
}

type ClientEvent struct {
	Type      string
	Start     *StartEvent
	Resume    *ResumeEvent
	Audio     *AudioEvent
	Finish    *FinishEvent
	Heartbeat *HeartbeatEvent
}

type ReadyEvent struct {
	Type                string    `json:"type"`
	ProtocolVersion     string    `json:"protocol_version"`
	SessionID           string    `json:"session_id"`
	NextSequence        int64     `json:"next_sequence"`
	MaxFrameBytes       int       `json:"max_frame_bytes"`
	HeartbeatIntervalMS int64     `json:"heartbeat_interval_ms"`
	ExpiresAt           time.Time `json:"expires_at"`
}

type AckEvent struct {
	Type                 string `json:"type"`
	SessionID            string `json:"session_id"`
	AcknowledgedSequence int64  `json:"acknowledged_sequence"`
	ReceivedBytes        int64  `json:"received_bytes"`
}

type HeartbeatAckEvent struct {
	Type      string    `json:"type"`
	SessionID string    `json:"session_id"`
	ServerAt  time.Time `json:"server_at"`
}

type PartialTranscriptEvent struct {
	Type           string `json:"type"`
	SessionID      string `json:"session_id"`
	Revision       int64  `json:"revision"`
	Text           string `json:"text"`
	FinalThroughMS int64  `json:"final_through_ms"`
}

type FinalTranscriptEvent struct {
	Type       string `json:"type"`
	SessionID  string `json:"session_id"`
	Text       string `json:"text"`
	Language   string `json:"language"`
	ProviderID string `json:"provider_id"`
}

type ErrorEvent struct {
	Type             string `json:"type"`
	SessionID        string `json:"session_id,omitempty"`
	Code             string `json:"code"`
	Message          string `json:"message"`
	Retriable        bool   `json:"retriable"`
	ExpectedSequence *int64 `json:"expected_sequence,omitempty"`
}

type ClosedEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Reason    string `json:"reason"`
}

func EncodeServerEvent(event any) ([]byte, error) {
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encode realtime server event: %w", err)
	}
	if len(encoded) > MaxServerMessageBytes {
		return nil, ErrInvalidEvent
	}
	return encoded, nil
}

func DecodeClientEvent(data []byte) (ClientEvent, error) {
	if len(data) == 0 || len(data) > MaxClientMessageBytes || !utf8.Valid(data) {
		return ClientEvent{}, ErrInvalidEvent
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return ClientEvent{}, ErrInvalidEvent
	}
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Type == "" {
		return ClientEvent{}, ErrInvalidEvent
	}
	switch envelope.Type {
	case EventStart:
		var event StartEvent
		if err := decodeStrict(data, &event); err != nil || event.validate() != nil {
			return ClientEvent{}, ErrInvalidEvent
		}
		return ClientEvent{Type: event.Type, Start: &event}, nil
	case EventResume:
		var event ResumeEvent
		if err := decodeStrict(data, &event); err != nil || event.validate() != nil {
			return ClientEvent{}, ErrInvalidEvent
		}
		return ClientEvent{Type: event.Type, Resume: &event}, nil
	case EventAudio:
		var event AudioEvent
		if err := decodeStrict(data, &event); err != nil || event.validate() != nil {
			return ClientEvent{}, ErrInvalidEvent
		}
		return ClientEvent{Type: event.Type, Audio: &event}, nil
	case EventFinish:
		var event FinishEvent
		if err := decodeStrict(data, &event); err != nil || event.validate() != nil {
			return ClientEvent{}, ErrInvalidEvent
		}
		return ClientEvent{Type: event.Type, Finish: &event}, nil
	case EventHeartbeat:
		var event HeartbeatEvent
		if err := decodeStrict(data, &event); err != nil || event.validate() != nil {
			return ClientEvent{}, ErrInvalidEvent
		}
		return ClientEvent{Type: event.Type, Heartbeat: &event}, nil
	default:
		return ClientEvent{}, ErrInvalidEvent
	}
}

func (event StartEvent) validate() error {
	if event.Type != EventStart || event.ProtocolVersion != ProtocolVersion ||
		!canonicalUUID(event.ClientSessionID) || !validIdempotencyKey(event.IdempotencyKey) ||
		event.Encoding != EncodingPCMS16LE || event.Channels != 1 ||
		event.FrameDurationMS < 20 || event.FrameDurationMS > 100 ||
		!validSampleRate(event.SampleRateHz) || !languageTagPattern.MatchString(event.Language) ||
		(event.ProviderProfileID != "" && !canonicalUUID(event.ProviderProfileID)) ||
		(event.HotwordSetID != "" && !canonicalUUID(event.HotwordSetID)) {
		return ErrInvalidEvent
	}
	if event.SampleRateHz*event.Channels*2*event.FrameDurationMS/1000 > MaxFrameBytes {
		return ErrInvalidEvent
	}
	return nil
}

func (event ResumeEvent) validate() error {
	if event.Type != EventResume || event.ProtocolVersion != ProtocolVersion ||
		!canonicalUUID(event.SessionID) || event.LastAcknowledgedSequence < -1 {
		return ErrInvalidEvent
	}
	return nil
}

func (event AudioEvent) validate() error {
	if event.Type != EventAudio || !canonicalUUID(event.SessionID) ||
		event.Sequence < 0 || event.CapturedAtMS < 0 {
		return ErrInvalidEvent
	}
	pcm, err := event.PCM()
	if err != nil || len(pcm) == 0 || len(pcm) > MaxFrameBytes || len(pcm)%2 != 0 {
		return ErrInvalidEvent
	}
	return nil
}

func (event AudioEvent) PCM() ([]byte, error) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(event.PCMBase64)
	if err != nil {
		return nil, fmt.Errorf("%w: decode PCM", ErrInvalidEvent)
	}
	return decoded, nil
}

func (event FinishEvent) validate() error {
	if event.Type != EventFinish || !canonicalUUID(event.SessionID) ||
		event.FinalSequence < -1 || event.CapturedDurationMS < 0 ||
		!canonicalSHA256.MatchString(event.ClientArchiveSHA256) {
		return ErrInvalidEvent
	}
	return nil
}

func (event HeartbeatEvent) validate() error {
	if event.Type != EventHeartbeat || !canonicalUUID(event.SessionID) || event.SentAtMS < 0 {
		return ErrInvalidEvent
	}
	return nil
}

func validSampleRate(value int) bool {
	switch value {
	case 8000, 16000, 24000, 48000:
		return true
	default:
		return false
	}
}

func canonicalUUID(value string) bool {
	normalized, ok := identifier.NormalizeUUID(value)
	return ok && normalized == value
}

func validIdempotencyKey(value string) bool {
	if len(value) < 1 || len(value) > 200 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := consumeJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate object key")
			}
			seen[key] = struct{}{}
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}
