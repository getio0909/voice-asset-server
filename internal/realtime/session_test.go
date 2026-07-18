package realtime

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testWorkspaceID = "20000000-0000-4000-8000-000000000001"
	testUserID      = "30000000-0000-4000-8000-000000000001"
)

func TestSessionSequencesReconnectsAndFinalizes(t *testing.T) {
	startedAt := time.Date(2026, 7, 17, 14, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionParams{
		ID: testSessionID, WorkspaceID: testWorkspaceID, CreatedBy: testUserID,
		Start: validStartEvent(), Now: startedAt,
	})
	if err != nil || session.State != StateStreaming || session.AcknowledgedSequence() != -1 {
		t.Fatalf("NewSession() = (%+v, %v)", session, err)
	}
	frame := audioEvent(0, make([]byte, 640))
	session, disposition, err := session.ApplyAudio(frame, startedAt.Add(20*time.Millisecond))
	if err != nil || disposition != FrameAccepted || session.NextSequence != 1 || session.ReceivedBytes != 640 {
		t.Fatalf("ApplyAudio() = (%+v, %q, %v)", session, disposition, err)
	}
	replayed, disposition, err := session.ApplyAudio(frame, startedAt.Add(30*time.Millisecond))
	if err != nil || disposition != FrameDuplicate || replayed.Version != session.Version {
		t.Fatalf("ApplyAudio(replay) = (%+v, %q, %v)", replayed, disposition, err)
	}
	if _, _, err := session.ApplyAudio(audioEvent(2, make([]byte, 640)), startedAt.Add(40*time.Millisecond)); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("ApplyAudio(gap) error = %v", err)
	}
	session, err = session.Interrupt(startedAt.Add(time.Second), 0)
	if err != nil || session.State != StateInterrupted || session.ReconnectBy == nil {
		t.Fatalf("Interrupt() = (%+v, %v)", session, err)
	}
	session, err = session.Resume(ResumeEvent{
		Type: EventResume, ProtocolVersion: ProtocolVersion, SessionID: testSessionID,
		LastAcknowledgedSequence: -1,
	}, startedAt.Add(2*time.Second))
	if err != nil || session.State != StateStreaming || session.ReconnectBy != nil {
		t.Fatalf("Resume() = (%+v, %v)", session, err)
	}
	session, _, err = session.ApplyAudio(audioEvent(1, make([]byte, 320)), startedAt.Add(3*time.Second))
	if err != nil || session.AcknowledgedSequence() != 1 || session.ReceivedBytes != 960 {
		t.Fatalf("ApplyAudio(short final frame) = (%+v, %v)", session, err)
	}
	session, err = session.BeginFinalization(FinishEvent{
		Type: EventFinish, SessionID: testSessionID, FinalSequence: 1,
		CapturedDurationMS: 30, ClientArchiveSHA256: strings.Repeat("a", 64),
	}, startedAt.Add(4*time.Second))
	if err != nil || session.State != StateFinalizing {
		t.Fatalf("BeginFinalization() = (%+v, %v)", session, err)
	}
	session, err = session.Complete(ProviderResult{
		Text: "final transcript", Language: "en-US", ProviderID: "mock_asr",
	}, startedAt.Add(5*time.Second))
	if err != nil || session.State != StateCompleted || session.FinalTranscript != "final transcript" ||
		session.FinalLanguage != "en-US" || session.FinalProviderID != "mock_asr" || session.CompletedAt == nil {
		t.Fatalf("Complete() = (%+v, %v)", session, err)
	}
}

func TestSessionFailsClosedOnStateTimingAndFrameErrors(t *testing.T) {
	startedAt := time.Date(2026, 7, 17, 14, 0, 0, 0, time.UTC)
	session, err := NewSession(NewSessionParams{
		ID: testSessionID, WorkspaceID: testWorkspaceID, CreatedBy: testUserID,
		Start: validStartEvent(), Now: startedAt, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongSession := audioEvent(0, make([]byte, 640))
	wrongSession.SessionID = "10000000-0000-4000-8000-000000000099"
	if _, _, err := session.ApplyAudio(wrongSession, startedAt); !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("wrong session error = %v", err)
	}
	if _, _, err := session.ApplyAudio(audioEvent(0, make([]byte, 642)), startedAt); !errors.Is(err, ErrFrameSize) {
		t.Fatalf("oversized frame error = %v", err)
	}
	first, _, err := session.ApplyAudio(audioEvent(0, make([]byte, 640)), startedAt.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	backwardTime := audioEvent(1, make([]byte, 640))
	backwardTime.CapturedAtMS = -1
	if _, _, err := first.ApplyAudio(backwardTime, startedAt.Add(2*time.Millisecond)); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("backward capture time error = %v", err)
	}
	if _, err := session.BeginFinalization(FinishEvent{
		Type: EventFinish, SessionID: testSessionID, FinalSequence: 0,
		ClientArchiveSHA256: strings.Repeat("a", 64),
	}, startedAt); !errors.Is(err, ErrFinalSequence) {
		t.Fatalf("finish with missing frame error = %v", err)
	}
	interrupted, err := session.Interrupt(startedAt, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := interrupted.Resume(ResumeEvent{
		Type: EventResume, ProtocolVersion: ProtocolVersion, SessionID: testSessionID,
		LastAcknowledgedSequence: -1,
	}, startedAt.Add(2*time.Second)); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("late resume error = %v", err)
	}
	if _, _, err := session.ApplyAudio(audioEvent(0, make([]byte, 640)), startedAt.Add(time.Minute)); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired frame error = %v", err)
	}
	expired, err := session.Expire(startedAt.Add(time.Minute))
	if err != nil || expired.State != StateExpired {
		t.Fatalf("Expire() = (%+v, %v)", expired, err)
	}
	if _, err := expired.Fail("internal_error"); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("terminal Fail() error = %v", err)
	}
	if _, err := session.Fail("raw provider secret"); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("unsafe Fail() error = %v", err)
	}
}

func validStartEvent() StartEvent {
	return StartEvent{
		Type: EventStart, ProtocolVersion: ProtocolVersion,
		ClientSessionID: "10000000-0000-4000-8000-000000000002",
		IdempotencyKey:  "realtime-recording-1", Encoding: EncodingPCMS16LE,
		SampleRateHz: 16000, Channels: 1, FrameDurationMS: 20, Language: "en-US",
	}
}

func audioEvent(sequence int64, pcm []byte) AudioEvent {
	return AudioEvent{
		Type: EventAudio, SessionID: testSessionID, Sequence: sequence,
		CapturedAtMS: sequence * 20, PCMBase64: base64.StdEncoding.EncodeToString(pcm),
	}
}
