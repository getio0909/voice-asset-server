package realtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

const generatedControllerSessionID = "00000000-0000-4000-8000-000000000000"

func TestControllerCompletesMockRealtimeFlow(t *testing.T) {
	controller, repository, hub, principal, _ := newControllerFixture()
	reads := [][]byte{mustRealtimeJSON(t, validStartEvent())}
	reads = append(reads, mustRealtimeJSON(t, HeartbeatEvent{
		Type: EventHeartbeat, SessionID: generatedControllerSessionID, SentAtMS: 0,
	}))
	pcm := base64.StdEncoding.EncodeToString(make([]byte, 640))
	for sequence := int64(0); sequence < 50; sequence++ {
		reads = append(reads, mustRealtimeJSON(t, AudioEvent{
			Type: EventAudio, SessionID: generatedControllerSessionID,
			Sequence: sequence, CapturedAtMS: sequence * 20, PCMBase64: pcm,
		}))
	}
	reads = append(reads, mustRealtimeJSON(t, FinishEvent{
		Type: EventFinish, SessionID: generatedControllerSessionID, FinalSequence: 49,
		CapturedDurationMS: 1000, ClientArchiveSHA256: strings.Repeat("a", 64),
	}))
	transport := &scriptedEventTransport{reads: reads}

	if err := controller.Serve(context.Background(), principal, transport); err != nil {
		t.Fatal(err)
	}
	if repository.session.State != StateCompleted ||
		repository.session.FinalTranscript != "Welcome to VoiceAsset." || hub.Size() != 0 {
		t.Fatalf("completed session = %+v, hub size = %d", repository.session, hub.Size())
	}

	wantCounts := map[string]int{
		"ready": 1, "heartbeat_ack": 1, "ack": 50, "partial_transcript": 2,
		"final_transcript": 1, "closed": 1,
	}
	gotCounts := make(map[string]int)
	for _, payload := range transport.writes {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatalf("decode server event %q: %v", payload, err)
		}
		gotCounts[envelope.Type]++
	}
	if len(transport.writes) != 56 || !equalEventCounts(gotCounts, wantCounts) {
		t.Fatalf("server event counts = %v across %d writes, want %v", gotCounts, len(transport.writes), wantCounts)
	}
}

func TestControllerBoundsInitialEventWait(t *testing.T) {
	controller, repository, hub, principal, _ := newControllerFixture()
	controller.heartbeatInterval = 5 * time.Millisecond
	transport := &deadlineEventTransport{}

	started := time.Now()
	err := controller.Serve(context.Background(), principal, transport)

	if !errors.Is(err, ErrRealtimeTransport) {
		t.Fatalf("Serve() error = %v", err)
	}
	if transport.readDeadlines != 1 || time.Since(started) > time.Second {
		t.Fatalf("bounded reads/elapsed = %d/%s", transport.readDeadlines, time.Since(started))
	}
	if repository.session.ID != "" || hub.Size() != 0 {
		t.Fatalf("unexpected pre-session state = %+v, hub size = %d", repository.session, hub.Size())
	}
}

func TestControllerInterruptsIdleStreamForBoundedReconnect(t *testing.T) {
	controller, repository, hub, principal, _ := newControllerFixture()
	controller.heartbeatInterval = 5 * time.Millisecond
	transport := &deadlineEventTransport{reads: [][]byte{mustRealtimeJSON(t, validStartEvent())}}

	err := controller.Serve(context.Background(), principal, transport)

	if !errors.Is(err, ErrRealtimeTransport) {
		t.Fatalf("Serve() error = %v", err)
	}
	if transport.readDeadlines != 2 || len(transport.writes) != 1 {
		t.Fatalf("bounded reads/writes = %d/%d", transport.readDeadlines, len(transport.writes))
	}
	if repository.session.State != StateInterrupted || repository.session.ReconnectBy == nil || hub.Size() != 1 {
		t.Fatalf("idle session = %+v, hub size = %d", repository.session, hub.Size())
	}
}

func TestControllerResumesRetainedProviderAfterLostAcknowledgement(t *testing.T) {
	controller, repository, hub, principal, now := newControllerFixture()
	pcm := base64.StdEncoding.EncodeToString(make([]byte, 640))
	first := &scriptedEventTransport{reads: [][]byte{
		mustRealtimeJSON(t, validStartEvent()),
		mustRealtimeJSON(t, AudioEvent{
			Type: EventAudio, SessionID: generatedControllerSessionID,
			Sequence: 0, CapturedAtMS: 0, PCMBase64: pcm,
		}),
	}}
	if err := controller.Serve(context.Background(), principal, first); !errors.Is(err, ErrRealtimeTransport) {
		t.Fatalf("first Serve() error = %v", err)
	}
	if repository.session.State != StateInterrupted || repository.session.NextSequence != 1 || hub.Size() != 1 {
		t.Fatalf("interrupted session = %+v, hub size = %d", repository.session, hub.Size())
	}

	*now = now.Add(time.Second)
	second := &scriptedEventTransport{reads: [][]byte{
		mustRealtimeJSON(t, ResumeEvent{
			Type: EventResume, ProtocolVersion: ProtocolVersion,
			SessionID: generatedControllerSessionID, LastAcknowledgedSequence: -1,
		}),
		mustRealtimeJSON(t, FinishEvent{
			Type: EventFinish, SessionID: generatedControllerSessionID, FinalSequence: 0,
			CapturedDurationMS: 20, ClientArchiveSHA256: strings.Repeat("b", 64),
		}),
	}}
	if err := controller.Serve(context.Background(), principal, second); err != nil {
		t.Fatal(err)
	}
	if repository.session.State != StateCompleted || repository.session.NextSequence != 1 || hub.Size() != 0 {
		t.Fatalf("resumed session = %+v, hub size = %d", repository.session, hub.Size())
	}
	var ready ReadyEvent
	if err := json.Unmarshal(second.writes[0], &ready); err != nil || ready.Type != "ready" || ready.NextSequence != 1 {
		t.Fatalf("resume ready = (%+v, %v)", ready, err)
	}
}

func TestControllerRejectsAmbiguousFirstEventWithoutEchoingInput(t *testing.T) {
	controller, _, _, principal, _ := newControllerFixture()
	transport := &scriptedEventTransport{reads: [][]byte{
		[]byte(`{"type":"start","type":"finish","secret":"top-secret-value"}`),
	}}
	if err := controller.Serve(context.Background(), principal, transport); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(transport.writes) != 1 || bytes.Contains(transport.writes[0], []byte("top-secret-value")) {
		t.Fatalf("unsafe response events = %q", transport.writes)
	}
	var event ErrorEvent
	if err := json.Unmarshal(transport.writes[0], &event); err != nil || event.Code != "invalid_event" || event.SessionID != "" {
		t.Fatalf("error event = (%+v, %v)", event, err)
	}
	if bytes.Contains(transport.writes[0], []byte("session_id")) {
		t.Fatalf("pre-session error contains session_id: %s", transport.writes[0])
	}
}

func TestControllerReplaysCompletedResultWhenFinalResponseWasLost(t *testing.T) {
	controller, repository, hub, principal, now := newControllerFixture()
	pcm := base64.StdEncoding.EncodeToString(make([]byte, 640))
	first := &scriptedEventTransport{
		reads: [][]byte{
			mustRealtimeJSON(t, validStartEvent()),
			mustRealtimeJSON(t, AudioEvent{
				Type: EventAudio, SessionID: generatedControllerSessionID,
				Sequence: 0, CapturedAtMS: 0, PCMBase64: pcm,
			}),
			mustRealtimeJSON(t, FinishEvent{
				Type: EventFinish, SessionID: generatedControllerSessionID, FinalSequence: 0,
				CapturedDurationMS: 20, ClientArchiveSHA256: strings.Repeat("c", 64),
			}),
		},
		failWriteAt: 2,
	}
	if err := controller.Serve(context.Background(), principal, first); !errors.Is(err, ErrRealtimeTransport) {
		t.Fatalf("first Serve() error = %v", err)
	}
	if repository.session.State != StateCompleted || hub.Size() != 0 {
		t.Fatalf("completed session = %+v, hub size = %d", repository.session, hub.Size())
	}

	*now = now.Add(time.Second)
	second := &scriptedEventTransport{reads: [][]byte{
		mustRealtimeJSON(t, ResumeEvent{
			Type: EventResume, ProtocolVersion: ProtocolVersion,
			SessionID: generatedControllerSessionID, LastAcknowledgedSequence: -1,
		}),
	}}
	if err := controller.Serve(context.Background(), principal, second); err != nil {
		t.Fatal(err)
	}
	gotTypes := make([]string, 0, len(second.writes))
	for _, payload := range second.writes {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatal(err)
		}
		gotTypes = append(gotTypes, envelope.Type)
	}
	if strings.Join(gotTypes, ",") != "ready,final_transcript,closed" {
		t.Fatalf("replayed event types = %v", gotTypes)
	}
}

func newControllerFixture() (
	*Controller,
	*fakeRealtimeRepository,
	*StreamHub,
	auth.Principal,
	*time.Time,
) {
	repository := &fakeRealtimeRepository{}
	service := NewService(repository)
	service.random = bytes.NewReader(make([]byte, 1024))
	now := time.Date(2026, 7, 17, 16, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	hub := NewStreamHub(MockStreamFactory{})
	controller := NewController(service, hub)
	controller.now = func() time.Time { return now }
	return controller, repository, hub, realtimePrincipal(testWorkspaceID, testUserID), &now
}

func mustRealtimeJSON(t *testing.T, event any) []byte {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func equalEventCounts(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for eventType, count := range want {
		if got[eventType] != count {
			return false
		}
	}
	return true
}

type scriptedEventTransport struct {
	reads       [][]byte
	writes      [][]byte
	failWriteAt int
}

type deadlineEventTransport struct {
	reads         [][]byte
	writes        [][]byte
	readDeadlines int
}

func (transport *deadlineEventTransport) Read(ctx context.Context) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("realtime read has no deadline")
	}
	transport.readDeadlines++
	if len(transport.reads) != 0 {
		payload := append([]byte(nil), transport.reads[0]...)
		transport.reads = transport.reads[1:]
		return payload, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (transport *deadlineEventTransport) Write(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	transport.writes = append(transport.writes, append([]byte(nil), payload...))
	return nil
}

func (transport *scriptedEventTransport) Read(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(transport.reads) == 0 {
		return nil, io.EOF
	}
	payload := append([]byte(nil), transport.reads[0]...)
	transport.reads = transport.reads[1:]
	return payload, nil
}

func (transport *scriptedEventTransport) Write(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if transport.failWriteAt > 0 && len(transport.writes) == transport.failWriteAt {
		return io.ErrClosedPipe
	}
	transport.writes = append(transport.writes, append([]byte(nil), payload...))
	return nil
}
