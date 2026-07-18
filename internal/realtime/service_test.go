package realtime

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestServicePersistsIdempotentRealtimeLifecycle(t *testing.T) {
	repository := &fakeRealtimeRepository{}
	service := NewService(repository)
	now := time.Date(2026, 7, 17, 15, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	principal := realtimePrincipal(testWorkspaceID, testUserID)

	started, replayed, err := service.Start(context.Background(), principal, validStartEvent())
	if err != nil || replayed || started.State != StateStreaming {
		t.Fatalf("Start() = (%+v, %t, %v)", started, replayed, err)
	}
	replayedSession, replayed, err := service.Start(context.Background(), principal, validStartEvent())
	if err != nil || !replayed || replayedSession.ID != started.ID {
		t.Fatalf("Start(replay) = (%+v, %t, %v)", replayedSession, replayed, err)
	}
	conflicting := validStartEvent()
	conflicting.Language = "zh-CN"
	if _, _, err := service.Start(context.Background(), principal, conflicting); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("Start(conflict) error = %v", err)
	}

	frame := AudioEvent{
		Type: EventAudio, SessionID: started.ID, Sequence: 0, CapturedAtMS: 0,
		PCMBase64: base64.StdEncoding.EncodeToString(make([]byte, 640)),
	}
	now = now.Add(20 * time.Millisecond)
	accepted, disposition, err := service.AcceptAudio(context.Background(), principal, frame)
	if err != nil || disposition != FrameAccepted || accepted.NextSequence != 1 {
		t.Fatalf("AcceptAudio() = (%+v, %q, %v)", accepted, disposition, err)
	}
	replayedFrame, disposition, err := service.AcceptAudio(context.Background(), principal, frame)
	if err != nil || disposition != FrameDuplicate || replayedFrame.Version != accepted.Version {
		t.Fatalf("AcceptAudio(replay) = (%+v, %q, %v)", replayedFrame, disposition, err)
	}
	gap := frame
	gap.Sequence = 2
	if _, _, err := service.AcceptAudio(context.Background(), principal, gap); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("AcceptAudio(gap) error = %v", err)
	}

	now = now.Add(time.Second)
	interrupted, err := service.Interrupt(context.Background(), principal, started.ID)
	if err != nil || interrupted.State != StateInterrupted {
		t.Fatalf("Interrupt() = (%+v, %v)", interrupted, err)
	}
	now = now.Add(time.Second)
	resumed, err := service.Resume(context.Background(), principal, ResumeEvent{
		Type: EventResume, ProtocolVersion: ProtocolVersion, SessionID: started.ID,
		LastAcknowledgedSequence: -1,
	})
	if err != nil || resumed.State != StateStreaming || resumed.NextSequence != 1 {
		t.Fatalf("Resume() = (%+v, %v)", resumed, err)
	}
	now = now.Add(time.Second)
	finalizing, err := service.BeginFinalization(context.Background(), principal, FinishEvent{
		Type: EventFinish, SessionID: started.ID, FinalSequence: 0,
		CapturedDurationMS: 20, ClientArchiveSHA256: strings.Repeat("a", 64),
	})
	if err != nil || finalizing.State != StateFinalizing {
		t.Fatalf("BeginFinalization() = (%+v, %v)", finalizing, err)
	}
	now = now.Add(time.Second)
	completed, err := service.Complete(context.Background(), principal, started.ID, ProviderResult{
		Text: "durable local fallback pending", Language: "en-US", ProviderID: "mock_asr",
	})
	if err != nil || completed.State != StateCompleted {
		t.Fatalf("Complete() = (%+v, %v)", completed, err)
	}

	wantActions := []string{
		AuditSessionStarted, AuditSessionInterrupted, AuditSessionResumed,
		AuditSessionFinalizing, AuditSessionCompleted,
	}
	if len(repository.audits) != len(wantActions) {
		t.Fatalf("audit count = %d, want %d", len(repository.audits), len(wantActions))
	}
	for index, action := range wantActions {
		if repository.audits[index].Action != action {
			t.Fatalf("audit %d action = %q, want %q", index, repository.audits[index].Action, action)
		}
		if strings.Contains(string(repository.audits[index].Metadata), completed.FinalTranscript) {
			t.Fatal("audit metadata contains transcript text")
		}
	}
}

func TestServiceEnforcesScopeWorkspaceAndOptimisticVersion(t *testing.T) {
	repository := &fakeRealtimeRepository{}
	service := NewService(repository)
	service.now = func() time.Time { return time.Date(2026, 7, 17, 15, 0, 0, 0, time.UTC) }
	viewer := auth.Principal{
		WorkspaceID: testWorkspaceID, UserID: testUserID,
		Scopes: []string{auth.ScopeTranscriptsRead},
	}
	if _, _, err := service.Start(context.Background(), viewer, validStartEvent()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Start(viewer) error = %v", err)
	}
	principal := realtimePrincipal(testWorkspaceID, testUserID)
	started, _, err := service.Start(context.Background(), principal, validStartEvent())
	if err != nil {
		t.Fatal(err)
	}
	other := realtimePrincipal("20000000-0000-4000-8000-000000000099", testUserID)
	if _, err := service.Get(context.Background(), other, started.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(other workspace) error = %v", err)
	}
	repository.forceConflict = true
	frame := AudioEvent{
		Type: EventAudio, SessionID: started.ID, Sequence: 0, CapturedAtMS: 0,
		PCMBase64: base64.StdEncoding.EncodeToString(make([]byte, 640)),
	}
	if _, _, err := service.AcceptAudio(context.Background(), principal, frame); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("AcceptAudio(conflict) error = %v", err)
	}
}

func realtimePrincipal(workspaceID, userID string) auth.Principal {
	return auth.Principal{
		WorkspaceID: workspaceID, UserID: userID,
		Scopes: []string{auth.ScopeTranscriptionsWrite},
	}
}

type fakeRealtimeRepository struct {
	session       Session
	requestHash   string
	audits        []Audit
	forceConflict bool
}

func (repository *fakeRealtimeRepository) Create(
	_ context.Context,
	session Session,
	requestHash string,
	audit Audit,
) (Session, bool, error) {
	if repository.session.ID != "" {
		if repository.requestHash != requestHash {
			return Session{}, false, ErrIdempotencyConflict
		}
		return repository.session, true, nil
	}
	repository.session = session
	repository.requestHash = requestHash
	repository.audits = append(repository.audits, audit)
	return session, false, nil
}

func (repository *fakeRealtimeRepository) Get(
	_ context.Context,
	workspaceID,
	sessionID string,
) (Session, error) {
	if repository.session.ID != sessionID || repository.session.WorkspaceID != workspaceID {
		return Session{}, ErrNotFound
	}
	return repository.session, nil
}

func (repository *fakeRealtimeRepository) Update(
	_ context.Context,
	expectedVersion int64,
	session Session,
	audit *Audit,
) (Session, error) {
	if repository.forceConflict || repository.session.Version != expectedVersion || session.Version != expectedVersion+1 {
		return Session{}, ErrVersionConflict
	}
	repository.session = session
	if audit != nil {
		repository.audits = append(repository.audits, *audit)
	}
	return session, nil
}
