package review

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

const reviewRevisionID = "11111111-1111-4111-8111-111111111111"

type fakeRepository struct {
	decision DecisionParams
	approval ApprovalParams
}

func (repository *fakeRepository) AddDecision(_ context.Context, params DecisionParams) (Record, error) {
	repository.decision = params
	return Record{ID: params.ID, RevisionID: params.RevisionID, Action: params.Action}, nil
}
func (repository *fakeRepository) Approve(_ context.Context, params ApprovalParams) (ApprovalResult, error) {
	repository.approval = params
	return ApprovalResult{ApprovedRevision: transcript.Revision{ID: params.ApprovedRevisionID}}, nil
}

type fakeRevisionReader struct {
	revision transcript.Revision
	err      error
}

func (reader fakeRevisionReader) GetRevision(context.Context, string, string) (transcript.Revision, error) {
	return reader.revision, reader.err
}

type sequentialReader struct{ next byte }

func (reader *sequentialReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = reader.next
		reader.next++
	}
	return len(value), nil
}

func reviewer(scopes ...string) auth.Principal {
	return auth.Principal{UserID: "user", WorkspaceID: "workspace", Scopes: scopes}
}

func TestServiceValidatesAndRecordsIndividualDecision(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, fakeRevisionReader{})
	service.random = &sequentialReader{}
	index := 2
	record, err := service.AddDecision(context.Background(), reviewer(auth.ScopeCorrectionsWrite), reviewRevisionID,
		DecisionInput{Action: ActionAcceptChange, ChangeIndex: &index})
	if err != nil || record.Action != ActionAcceptChange {
		t.Fatalf("AddDecision() = %#v, %v", record, err)
	}
	if repository.decision.ChangeIndex == nil || *repository.decision.ChangeIndex != 2 ||
		repository.decision.WorkspaceID != "workspace" {
		t.Fatalf("decision params = %#v", repository.decision)
	}
	if _, err := service.AddDecision(context.Background(), reviewer(auth.ScopeCorrectionsWrite), reviewRevisionID,
		DecisionInput{Action: ActionRejectAll, ChangeIndex: &index}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid all decision error = %v", err)
	}
}

func TestServiceApprovalGeneratesTwoImmutableRevisionTimelines(t *testing.T) {
	repository := &fakeRepository{}
	source := transcript.Revision{
		ID: reviewRevisionID, ParentRevisionID: "parent", Kind: transcript.KindLLMCorrected,
		ReviewStatus: "pending", Segments: []transcript.Segment{{ID: "segment-1"}, {ID: "segment-2"}},
	}
	service := NewService(repository, fakeRevisionReader{revision: source})
	service.random = &sequentialReader{}
	result, err := service.Approve(context.Background(), reviewer(auth.ScopeCorrectionsWrite), reviewRevisionID,
		ApprovalInput{AcceptPending: true})
	if err != nil || result.ApprovedRevision.ID == "" {
		t.Fatalf("Approve() = %#v, %v", result, err)
	}
	params := repository.approval
	if len(params.HumanSegmentIDs) != 2 || len(params.ApprovedSegmentIDs) != 2 || !params.AcceptPending ||
		params.HumanRevisionID == params.ApprovedRevisionID {
		t.Fatalf("approval params = %#v", params)
	}
}

func TestServiceEnforcesCorrectionScopeAndTargetKind(t *testing.T) {
	service := NewService(&fakeRepository{}, fakeRevisionReader{revision: transcript.Revision{
		ID: reviewRevisionID, Kind: transcript.KindRawASR, Segments: []transcript.Segment{{ID: "segment"}},
	}})
	if _, err := service.Approve(context.Background(), reviewer(auth.ScopeTranscriptsRead), reviewRevisionID,
		ApprovalInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing scope error = %v", err)
	}
	if _, err := service.Approve(context.Background(), reviewer(auth.ScopeCorrectionsWrite), reviewRevisionID,
		ApprovalInput{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid kind error = %v", err)
	}
}

func TestServiceRejectsAlreadyAutoApprovedCorrection(t *testing.T) {
	service := NewService(&fakeRepository{}, fakeRevisionReader{revision: transcript.Revision{
		ID: reviewRevisionID, ParentRevisionID: "parent", Kind: transcript.KindLLMCorrected,
		ReviewStatus: "approved", Segments: []transcript.Segment{{ID: "segment"}},
	}})
	if _, err := service.Approve(context.Background(), reviewer(auth.ScopeCorrectionsWrite),
		reviewRevisionID, ApprovalInput{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("already approved error = %v", err)
	}
}

func TestBuildReviewedContentAcceptsOnlySelectedChanges(t *testing.T) {
	parent := transcript.Revision{
		Text: "Alpha wrong and beta bad.",
		Segments: []transcript.Segment{
			{ID: "one", Ordinal: 0, StartMS: 0, EndMS: 100, Text: "Alpha wrong", Words: json.RawMessage(`[]`)},
			{ID: "two", Ordinal: 1, StartMS: 101, EndMS: 200, Text: "beta bad", Words: json.RawMessage(`[]`)},
		},
	}
	corrected := transcript.Revision{Segments: []transcript.Segment{
		{ID: "corrected-one", Ordinal: 0, StartMS: 0, EndMS: 100, Text: "Alpha right", Words: json.RawMessage(`[]`)},
		{ID: "corrected-two", Ordinal: 1, StartMS: 101, EndMS: 200, Text: "beta good", Words: json.RawMessage(`[]`)},
	}}
	changes := []llm.Change{
		{SegmentID: "one", Original: "Alpha wrong", Replacement: "Alpha right"},
		{SegmentID: "two", Original: "beta bad", Replacement: "beta good"},
	}
	text, segments, accepted, err := buildReviewedContent(parent, corrected, changes,
		[]bool{true, false}, []string{"new-one", "new-two"})
	if err != nil || text != "Alpha right and beta bad." || segments[0].Text != "Alpha right" ||
		segments[1].Text != "beta bad" || len(accepted) != 1 || accepted[0] != 0 {
		t.Fatalf("reviewed content = %q, %#v, %#v, %v", text, segments, accepted, err)
	}
}
