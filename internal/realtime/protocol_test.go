package realtime

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

const testSessionID = "10000000-0000-4000-8000-000000000001"

func TestDecodeClientEventAcceptsStrictVersionedEvents(t *testing.T) {
	pcm := base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 3})
	tests := []struct {
		name, payload, eventType string
	}{
		{"start", `{"type":"start","protocol_version":"1","client_session_id":"10000000-0000-4000-8000-000000000002","idempotency_key":"recording-1","encoding":"pcm_s16le","sample_rate_hz":16000,"channels":1,"frame_duration_ms":20,"language":"zh-CN"}`, EventStart},
		{"resume", `{"type":"resume","protocol_version":"1","session_id":"` + testSessionID + `","last_acknowledged_sequence":-1}`, EventResume},
		{"audio", `{"type":"audio","session_id":"` + testSessionID + `","sequence":0,"captured_at_ms":0,"pcm_base64":"` + pcm + `"}`, EventAudio},
		{"finish", `{"type":"finish","session_id":"` + testSessionID + `","final_sequence":0,"captured_duration_ms":20,"client_archive_sha256":"` + strings.Repeat("a", 64) + `"}`, EventFinish},
		{"heartbeat", `{"type":"heartbeat","session_id":"` + testSessionID + `","sent_at_ms":20}`, EventHeartbeat},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, err := DecodeClientEvent([]byte(test.payload))
			if err != nil || event.Type != test.eventType {
				t.Fatalf("DecodeClientEvent() = (%+v, %v)", event, err)
			}
		})
	}
}

func TestDecodeClientEventRejectsAmbiguousOrUnsafeInput(t *testing.T) {
	validStart := `{"type":"start","protocol_version":"1","client_session_id":"10000000-0000-4000-8000-000000000002","idempotency_key":"recording-1","encoding":"pcm_s16le","sample_rate_hz":16000,"channels":1,"frame_duration_ms":20,"language":"en-US"}`
	tests := []struct {
		name, payload string
	}{
		{"unknown field", strings.Replace(validStart, `"language":"en-US"`, `"language":"en-US","secret":"no"`, 1)},
		{"duplicate key", strings.Replace(validStart, `"type":"start"`, `"type":"start","type":"finish"`, 1)},
		{"unsupported version", strings.Replace(validStart, `"protocol_version":"1"`, `"protocol_version":"2"`, 1)},
		{"uppercase UUID", strings.Replace(validStart, "10000000-0000-4000-8000-000000000002", "10000000-0000-4000-8000-00000000000A", 1)},
		{"control idempotency", strings.Replace(validStart, "recording-1", `recording\u000a1`, 1)},
		{"stereo", strings.Replace(validStart, `"channels":1`, `"channels":2`, 1)},
		{"invalid base64", `{"type":"audio","session_id":"` + testSessionID + `","sequence":0,"captured_at_ms":0,"pcm_base64":"***"}`},
		{"odd PCM", `{"type":"audio","session_id":"` + testSessionID + `","sequence":0,"captured_at_ms":0,"pcm_base64":"AQID"}`},
		{"trailing JSON", validStart + `{}`},
		{"oversized message", strings.Repeat("x", MaxClientMessageBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeClientEvent([]byte(test.payload)); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("DecodeClientEvent() error = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func TestRealtimeEventSchemaIsJSON(t *testing.T) {
	raw, err := os.ReadFile("../../contracts/realtime-events.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Dialect string                     `json:"$schema"`
		OneOf   []json.RawMessage          `json:"oneOf"`
		Defs    map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("decode realtime JSON Schema: %v", err)
	}
	if schema.Dialect != "https://json-schema.org/draft/2020-12/schema" ||
		len(schema.OneOf) != 12 || len(schema.Defs) < 12 || ProtocolVersion != "1" ||
		MaxFrameBytes != 64*1024 {
		t.Fatalf("unexpected realtime schema envelope: dialect=%q oneOf=%d defs=%d", schema.Dialect, len(schema.OneOf), len(schema.Defs))
	}
}
