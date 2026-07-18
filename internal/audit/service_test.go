package audit

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestRecordClassifiesAgentAndPassesSanitizedEntry(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x01}, 16))
	input := RecordInput{
		Principal: auth.Principal{
			UserID: "20000000-0000-4000-8000-000000000001", WorkspaceID: "10000000-0000-4000-8000-000000000001",
			Role: "agent",
		},
		Action: "transcript_revision.read", TargetType: "transcript_revision",
		TargetID: "50000000-0000-4000-8000-000000000001", RequestID: "request-1",
		Metadata: map[string]any{"segment_count": 2},
	}
	if err := service.Record(context.Background(), input); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if repository.entry.ActorType != "agent" || repository.entry.Action != input.Action ||
		repository.entry.TargetID != input.TargetID || string(repository.entry.Metadata) != `{"segment_count":2}` {
		t.Fatalf("entry = %+v", repository.entry)
	}
	if repository.entry.ID == "" {
		t.Fatal("audit identifier was not generated")
	}
}

func TestRecordRejectsInvalidIdentifiersBeforeRepository(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	err := service.Record(context.Background(), RecordInput{
		Principal: auth.Principal{UserID: "not-a-uuid", WorkspaceID: "workspace"},
		Action:    "asset.read", TargetType: "asset", TargetID: "also-invalid", RequestID: "request-1",
	})
	if !errors.Is(err, ErrInvalidInput) || repository.calls != 0 {
		t.Fatalf("Record() = %v, calls = %d", err, repository.calls)
	}
}

type fakeRepository struct {
	entry Entry
	err   error
	calls int
}

func (repository *fakeRepository) Record(_ context.Context, entry Entry) error {
	repository.calls++
	repository.entry = entry
	return repository.err
}
