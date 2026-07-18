// Package notification exposes immutable, personal task events to interactive sessions.
package notification

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const (
	defaultListLimit = 50
	maxListLimit     = 100
	maxCursorLength  = 1024
	cursorKind       = "personal_notifications"

	TypeJobSucceeded = "job.succeeded"
	TypeJobFailed    = "job.failed"
	TypeJobCancelled = "job.cancelled"

	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

var (
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid notification input")
)

// Event is a safe, immutable projection of one terminal job transition. It
// deliberately excludes the job payload and every credential-bearing field.
type Event struct {
	Sequence         int64     `json:"sequence"`
	ID               string    `json:"id"`
	Type             string    `json:"type"`
	JobID            string    `json:"job_id"`
	JobKind          string    `json:"job_kind"`
	State            string    `json:"state"`
	AssetID          *string   `json:"asset_id,omitempty"`
	ResultRevisionID *string   `json:"result_revision_id,omitempty"`
	ErrorCode        *string   `json:"error_code,omitempty"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type ListInput struct {
	Limit  int
	Cursor string
}

type ListResult struct {
	Items      []Event `json:"items"`
	NextCursor string  `json:"next_cursor"`
	HasMore    bool    `json:"has_more"`
}

type ListParams struct {
	WorkspaceID     string
	RecipientUserID string
	AfterSequence   int64
	Limit           int
}

type RepositoryPage struct {
	Items         []Event
	HighWatermark int64
}

type Repository interface {
	List(context.Context, ListParams) (RepositoryPage, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (service *Service) List(
	ctx context.Context,
	principal auth.Principal,
	input ListInput,
) (ListResult, error) {
	workspaceID, userID, err := authorize(principal)
	if err != nil {
		return ListResult{}, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return ListResult{}, ErrInvalidInput
	}
	afterSequence, err := decodeCursor(input.Cursor, workspaceID, userID)
	if err != nil {
		return ListResult{}, err
	}
	if service == nil || service.repository == nil {
		return ListResult{}, errors.New("notification repository is unavailable")
	}
	page, err := service.repository.List(ctx, ListParams{
		WorkspaceID: workspaceID, RecipientUserID: userID,
		AfterSequence: afterSequence, Limit: limit + 1,
	})
	if err != nil {
		return ListResult{}, fmt.Errorf("list notifications: %w", err)
	}
	if err := validatePage(page, afterSequence, limit+1); err != nil {
		return ListResult{}, err
	}
	items := page.Items
	hasMore := len(items) > limit
	if hasMore {
		items = append([]Event(nil), items[:limit]...)
	}
	checkpoint := page.HighWatermark
	if hasMore {
		checkpoint = items[len(items)-1].Sequence
	}
	if checkpoint < afterSequence {
		checkpoint = afterSequence
	}
	nextCursor, err := encodeCursor(workspaceID, userID, checkpoint)
	if err != nil {
		return ListResult{}, fmt.Errorf("encode notification cursor: %w", err)
	}
	if items == nil {
		items = make([]Event, 0)
	}
	return ListResult{Items: items, NextCursor: nextCursor, HasMore: hasMore}, nil
}

func authorize(principal auth.Principal) (string, string, error) {
	if principal.CredentialType != "session" || !principal.Can(auth.ScopeTranscriptsRead) {
		return "", "", ErrForbidden
	}
	workspaceID, workspaceOK := identifier.NormalizeUUID(principal.WorkspaceID)
	userID, userOK := identifier.NormalizeUUID(principal.UserID)
	if !workspaceOK || !userOK {
		return "", "", ErrForbidden
	}
	return workspaceID, userID, nil
}

func validatePage(page RepositoryPage, afterSequence int64, maximumItems int) error {
	if page.HighWatermark < afterSequence || page.HighWatermark < 0 || len(page.Items) > maximumItems {
		return errors.New("notification repository returned an invalid page")
	}
	previous := afterSequence
	for _, item := range page.Items {
		if item.Sequence <= previous || item.Sequence > page.HighWatermark {
			return errors.New("notification repository returned an invalid sequence")
		}
		previous = item.Sequence
	}
	return nil
}

type cursor struct {
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Sequence    int64  `json:"sequence"`
}

func encodeCursor(workspaceID, userID string, sequence int64) (string, error) {
	workspaceID, workspaceOK := identifier.NormalizeUUID(workspaceID)
	userID, userOK := identifier.NormalizeUUID(userID)
	if !workspaceOK || !userOK || sequence < 0 {
		return "", ErrInvalidInput
	}
	encoded, err := json.Marshal(cursor{
		Kind: cursorKind, WorkspaceID: workspaceID, UserID: userID, Sequence: sequence,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(value, workspaceID, userID string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	if len(value) > maxCursorLength {
		return 0, ErrInvalidInput
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var parsed cursor
	if err := decoder.Decode(&parsed); err != nil {
		return 0, ErrInvalidInput
	}
	if decoder.Decode(&struct{}{}) == nil || parsed.Kind != cursorKind ||
		parsed.WorkspaceID != workspaceID || parsed.UserID != userID || parsed.Sequence < 0 {
		return 0, ErrInvalidInput
	}
	return parsed.Sequence, nil
}
