// Package transcript exposes immutable transcript revisions and timelines.
package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

const (
	KindRawASR       = "raw_asr"
	KindNormalized   = "normalized"
	KindLLMCorrected = "llm_corrected"
	KindHumanEdited  = "human_edited"
	KindApproved     = "approved"
)

var (
	ErrForbidden  = errors.New("forbidden")
	ErrNotFound   = errors.New("transcript not found")
	ErrRepository = errors.New("transcript repository failure")
)

// Summary is the latest immutable revision for one asset transcript.
type Summary struct {
	ID                string    `json:"id"`
	AssetID           string    `json:"asset_id"`
	Language          string    `json:"language"`
	LatestRevisionID  string    `json:"latest_revision_id"`
	LatestKind        string    `json:"latest_kind"`
	LatestText        string    `json:"latest_text"`
	CreatedAt         time.Time `json:"created_at"`
	RevisionCreatedAt time.Time `json:"revision_created_at"`
}

// Revision is an immutable transcript snapshot with an integer-ms timeline.
type Revision struct {
	ID                  string          `json:"id"`
	TranscriptID        string          `json:"transcript_id"`
	AssetID             string          `json:"asset_id"`
	ParentRevisionID    string          `json:"parent_revision_id,omitempty"`
	Kind                string          `json:"kind"`
	Language            string          `json:"language"`
	Text                string          `json:"text"`
	ProviderSnapshot    json.RawMessage `json:"provider_snapshot"`
	HotwordSnapshot     json.RawMessage `json:"hotword_snapshot"`
	GlossarySnapshot    json.RawMessage `json:"glossary_snapshot"`
	Diff                json.RawMessage `json:"diff"`
	ValidationResult    json.RawMessage `json:"validation_result"`
	ProviderRawObjectID string          `json:"provider_raw_object_id,omitempty"`
	SourceJobID         string          `json:"source_job_id,omitempty"`
	CreatedBy           string          `json:"created_by,omitempty"`
	CreatedByType       string          `json:"created_by_type"`
	Model               string          `json:"model,omitempty"`
	PromptVersion       string          `json:"prompt_version,omitempty"`
	ReviewStatus        string          `json:"review_status"`
	CreatedAt           time.Time       `json:"created_at"`
	Segments            []Segment       `json:"segments"`
}

type Segment struct {
	ID         string          `json:"id"`
	Ordinal    int             `json:"ordinal"`
	StartMS    int64           `json:"start_ms"`
	EndMS      int64           `json:"end_ms"`
	Speaker    *string         `json:"speaker"`
	Text       string          `json:"text"`
	Confidence *float64        `json:"confidence"`
	Words      json.RawMessage `json:"words"`
}

type Repository interface {
	List(ctx context.Context, workspaceID, assetID string) ([]Summary, error)
	GetRevision(ctx context.Context, workspaceID, revisionID string) (Revision, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) List(ctx context.Context, principal auth.Principal, assetID string) ([]Summary, error) {
	if !principal.Can(auth.ScopeTranscriptsRead) {
		return nil, ErrForbidden
	}
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return nil, ErrNotFound
	}
	results, err := s.repository.List(ctx, principal.WorkspaceID, assetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("%w: list transcripts", ErrRepository)
	}
	return results, nil
}

func (s *Service) GetRevision(
	ctx context.Context,
	principal auth.Principal,
	revisionID string,
) (Revision, error) {
	if !principal.Can(auth.ScopeTranscriptsRead) {
		return Revision{}, ErrForbidden
	}
	revisionID = strings.TrimSpace(revisionID)
	if revisionID == "" {
		return Revision{}, ErrNotFound
	}
	result, err := s.repository.GetRevision(ctx, principal.WorkspaceID, revisionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Revision{}, ErrNotFound
		}
		return Revision{}, fmt.Errorf("%w: get revision", ErrRepository)
	}
	return result, nil
}
