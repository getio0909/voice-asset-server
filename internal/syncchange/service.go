// Package syncchange exposes an append-only, workspace-scoped asset change feed.
package syncchange

import (
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
	cursorKind       = "asset_changes"
)

var (
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid sync change input")
)

// AssetSnapshot is the non-secret asset state captured at mutation time.
type AssetSnapshot struct {
	ID           string     `json:"id"`
	CollectionID *string    `json:"collection_id"`
	Title        string     `json:"title"`
	Language     string     `json:"language"`
	Status       string     `json:"status"`
	DurationMS   *int64     `json:"duration_ms"`
	Version      int64      `json:"version"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	TrashedAt    *time.Time `json:"trashed_at"`
}

// Change is an immutable asset upsert or permanent-deletion event.
type Change struct {
	Sequence      int64          `json:"sequence"`
	EntityType    string         `json:"entity_type"`
	EntityID      string         `json:"entity_id"`
	Operation     string         `json:"operation"`
	EntityVersion int64          `json:"entity_version"`
	ChangedAt     time.Time      `json:"changed_at"`
	Asset         *AssetSnapshot `json:"asset,omitempty"`
}

type ListInput struct {
	Limit  int
	Cursor string
}

type ListResult struct {
	Items      []Change `json:"items"`
	NextCursor string   `json:"next_cursor"`
	HasMore    bool     `json:"has_more"`
}

type ListParams struct {
	WorkspaceID   string
	AfterSequence int64
	Limit         int
}

type RepositoryPage struct {
	Items         []Change
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
	workspaceID, err := authorize(principal)
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
	afterSequence, err := decodeCursor(input.Cursor, workspaceID)
	if err != nil {
		return ListResult{}, err
	}
	if service == nil || service.repository == nil {
		return ListResult{}, errors.New("sync change repository is unavailable")
	}
	page, err := service.repository.List(ctx, ListParams{
		WorkspaceID: workspaceID, AfterSequence: afterSequence, Limit: limit + 1,
	})
	if err != nil {
		return ListResult{}, fmt.Errorf("list sync changes: %w", err)
	}
	if page.HighWatermark < 0 || page.HighWatermark < afterSequence {
		return ListResult{}, errors.New("sync change repository returned an invalid watermark")
	}
	items := page.Items
	hasMore := len(items) > limit
	if hasMore {
		items = append([]Change(nil), items[:limit]...)
	}
	checkpoint := page.HighWatermark
	if hasMore {
		checkpoint = items[len(items)-1].Sequence
	}
	if checkpoint < afterSequence {
		checkpoint = afterSequence
	}
	nextCursor, err := encodeCursor(workspaceID, checkpoint)
	if err != nil {
		return ListResult{}, fmt.Errorf("encode sync cursor: %w", err)
	}
	if items == nil {
		items = make([]Change, 0)
	}
	return ListResult{Items: items, NextCursor: nextCursor, HasMore: hasMore}, nil
}

func authorize(principal auth.Principal) (string, error) {
	if !principal.Can(auth.ScopeAssetsRead) {
		return "", ErrForbidden
	}
	workspaceID, ok := identifier.NormalizeUUID(principal.WorkspaceID)
	if !ok {
		return "", ErrForbidden
	}
	return workspaceID, nil
}

type cursor struct {
	Kind        string `json:"kind"`
	WorkspaceID string `json:"workspace_id"`
	Sequence    int64  `json:"sequence"`
}

func encodeCursor(workspaceID string, sequence int64) (string, error) {
	if _, ok := identifier.NormalizeUUID(workspaceID); !ok || sequence < 0 {
		return "", ErrInvalidInput
	}
	encoded, err := json.Marshal(cursor{Kind: cursorKind, WorkspaceID: workspaceID, Sequence: sequence})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(value, workspaceID string) (int64, error) {
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
	var parsed cursor
	if err := json.Unmarshal(decoded, &parsed); err != nil ||
		parsed.Kind != cursorKind || parsed.WorkspaceID != workspaceID || parsed.Sequence < 0 {
		return 0, ErrInvalidInput
	}
	return parsed.Sequence, nil
}
