package correction

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

func validAutoApprovalCommit() CommitParams {
	validation, _ := json.Marshal(llm.ValidationResult{
		Valid: true, SchemaValid: true, OriginalsMatch: true, GlossarySupported: true,
		NumbersPreserved: true, UnitsPreserved: true, NegationsPreserved: true,
		TimelinePreserved: true, ChangeRatio: 0.1,
	})
	return CommitParams{
		JobID: "10000000-0000-4000-8000-000000000001", WorkerID: "worker",
		WorkspaceID:      "20000000-0000-4000-8000-000000000002",
		AssetID:          "30000000-0000-4000-8000-000000000003",
		ActorID:          "40000000-0000-4000-8000-000000000004",
		SourceRevisionID: "50000000-0000-4000-8000-000000000005",
		RevisionID:       "60000000-0000-4000-8000-000000000006",
		RawObjectID:      "70000000-0000-4000-8000-000000000007",
		AuditID:          "80000000-0000-4000-8000-000000000008",
		TranscriptID:     "90000000-0000-4000-8000-000000000009",
		Language:         "en-US", Text: "VoiceAsset", ProviderID: llm.MockProviderID,
		Model: "deterministic_glossary_v1", PromptVersion: llm.PromptVersionV1,
		ProviderSnapshot: json.RawMessage(`{"auto_approval_policy":"validated_glossary_only"}`),
		HotwordSnapshot:  json.RawMessage(`{}`),
		GlossarySnapshot: json.RawMessage(`{"sets":[{"id":"fixture","version":1}]}`),
		Diff:             json.RawMessage(`{"changes":[{"segment_id":"source-segment","original":"Voice Asset","replacement":"VoiceAsset","confidence":1,"reason":"glossary"}]}`),
		ValidationResult: validation,
		RawObject: storage.Object{
			Backend: storage.BackendLocal,
			Key:     "objects/raw", Size: 2, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Segments: []transcript.Segment{{
			ID: "a0000000-0000-4000-8000-00000000000a", Ordinal: 0,
			StartMS: 0, EndMS: 100, Text: "VoiceAsset", Words: json.RawMessage(`[]`),
		}},
		AutoApproval: &AutoApprovalParams{
			Policy:             llm.AutoApprovalGlossaryOnly,
			ReviewID:           "b0000000-0000-4000-8000-00000000000b",
			AuditID:            "c0000000-0000-4000-8000-00000000000c",
			HumanRevisionID:    "d0000000-0000-4000-8000-00000000000d",
			ApprovedRevisionID: "e0000000-0000-4000-8000-00000000000e",
			HumanSegmentIDs:    []string{"f0000000-0000-4000-8000-00000000000f"},
			ApprovedSegmentIDs: []string{"01000000-0000-4000-8000-000000000010"},
		},
		Now: time.Date(2026, 7, 16, 23, 0, 0, 0, time.UTC),
	}
}

func TestValidateCommitAcceptsStrictAutoApprovalEvidence(t *testing.T) {
	params := validAutoApprovalCommit()
	if err := validateCommit(params); err != nil {
		t.Fatalf("validateCommit() error = %v", err)
	}
}

func TestValidateCommitRejectsUnsafeAutoApprovalEvidence(t *testing.T) {
	tests := map[string]func(*CommitParams){
		"wrong policy":   func(params *CommitParams) { params.AutoApproval.Policy = llm.AutoApprovalNever },
		"empty glossary": func(params *CommitParams) { params.GlossarySnapshot = json.RawMessage(`{"sets":[]}`) },
		"empty changes":  func(params *CommitParams) { params.Diff = json.RawMessage(`{"changes":[]}`) },
		"failed validation": func(params *CommitParams) {
			params.ValidationResult = json.RawMessage(`{"valid":false}`)
		},
		"duplicate identifier": func(params *CommitParams) {
			params.AutoApproval.ApprovedRevisionID = params.RevisionID
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			params := validAutoApprovalCommit()
			mutate(&params)
			if err := validateCommit(params); !errors.Is(err, ErrInvalidCommit) {
				t.Fatalf("validateCommit() error = %v", err)
			}
		})
	}
}
