package realtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockStreamEmitsDeterministicPartialsAndFinal(t *testing.T) {
	session, err := NewSession(NewSessionParams{
		ID: testSessionID, WorkspaceID: testWorkspaceID, CreatedBy: testUserID,
		Start: validStartEvent(), Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := (MockStreamFactory{}).Open(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	var updates []*TranscriptUpdate
	for sequence := int64(0); sequence < 50; sequence++ {
		update, err := stream.Push(context.Background(), ProviderFrame{
			Sequence: sequence, CapturedAtMS: sequence * 20, PCM: make([]byte, 640),
		})
		if err != nil {
			t.Fatalf("Push(%d) error = %v", sequence, err)
		}
		if update != nil {
			updates = append(updates, update)
		}
	}
	if len(updates) != 2 || updates[0].Revision != 1 || updates[0].Text != "Welcome to" ||
		updates[0].FinalThroughMS != 500 || updates[1].Revision != 2 ||
		updates[1].Text != "Welcome to VoiceAsset." || updates[1].FinalThroughMS != 1000 {
		t.Fatalf("updates = %+v", updates)
	}
	result, err := stream.Finish(context.Background())
	if err != nil || result.Text != "Welcome to VoiceAsset." || result.Language != "en-US" ||
		result.ProviderID != asrMockProviderID {
		t.Fatalf("Finish() = (%+v, %v)", result, err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMockStreamRejectsGapsCancellationAndUseAfterClose(t *testing.T) {
	session, err := NewSession(NewSessionParams{
		ID: testSessionID, WorkspaceID: testWorkspaceID, CreatedBy: testUserID,
		Start: validStartEvent(), Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := (MockStreamFactory{}).Open(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Push(context.Background(), ProviderFrame{Sequence: 1, PCM: []byte{0, 0}}); !errors.Is(err, ErrSequenceGap) {
		t.Fatalf("Push(gap) error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := stream.Push(cancelled, ProviderFrame{Sequence: 0, PCM: []byte{0, 0}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Push(cancelled) error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Push(context.Background(), ProviderFrame{Sequence: 0, PCM: []byte{0, 0}}); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("Push(closed) error = %v", err)
	}
}

const asrMockProviderID = "mock_asr"
