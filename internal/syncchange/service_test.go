package syncchange

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

const (
	testWorkspaceID      = "10000000-0000-4000-8000-000000000091"
	testOtherWorkspaceID = "10000000-0000-4000-8000-000000000092"
)

func TestListPaginatesAndBindsCursorToWorkspace(t *testing.T) {
	changedAt := time.Date(2026, 7, 17, 20, 0, 0, 0, time.UTC)
	repository := &fakeRepository{page: RepositoryPage{
		Items: []Change{
			{Sequence: 1, EntityType: "asset", EntityID: "20000000-0000-4000-8000-000000000091", Operation: "upsert", EntityVersion: 1, ChangedAt: changedAt},
			{Sequence: 3, EntityType: "asset", EntityID: "20000000-0000-4000-8000-000000000092", Operation: "upsert", EntityVersion: 2, ChangedAt: changedAt},
			{Sequence: 8, EntityType: "asset", EntityID: "20000000-0000-4000-8000-000000000093", Operation: "delete", EntityVersion: 3, ChangedAt: changedAt},
		},
		HighWatermark: 8,
	}}
	service := NewService(repository)
	principal := readPrincipal(testWorkspaceID)

	first, err := service.List(context.Background(), principal, ListInput{Limit: 2})
	if err != nil || len(first.Items) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("List(first) = (%+v, %v)", first, err)
	}
	if repository.params != (ListParams{WorkspaceID: testWorkspaceID, AfterSequence: 0, Limit: 3}) {
		t.Fatalf("repository params = %+v", repository.params)
	}

	repository.page = RepositoryPage{
		Items:         []Change{{Sequence: 8, EntityType: "asset", Operation: "delete", EntityVersion: 3}},
		HighWatermark: 8,
	}
	second, err := service.List(context.Background(), principal, ListInput{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.HasMore || second.NextCursor == "" {
		t.Fatalf("List(second) = (%+v, %v)", second, err)
	}
	if repository.params.AfterSequence != 3 {
		t.Fatalf("after sequence = %d, want 3", repository.params.AfterSequence)
	}

	if _, err := service.List(context.Background(), readPrincipal(testOtherWorkspaceID), ListInput{
		Limit: 2, Cursor: first.NextCursor,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("List(other workspace cursor) error = %v", err)
	}
}

func TestListReturnsStableCheckpointForEmptyPage(t *testing.T) {
	repository := &fakeRepository{page: RepositoryPage{HighWatermark: 12}}
	result, err := NewService(repository).List(
		context.Background(), readPrincipal(testWorkspaceID), ListInput{},
	)
	if err != nil || result.Items == nil || len(result.Items) != 0 || result.HasMore || result.NextCursor == "" {
		t.Fatalf("List(empty) = (%+v, %v)", result, err)
	}
	repository.page = RepositoryPage{HighWatermark: 12}
	if _, err := NewService(repository).List(context.Background(), readPrincipal(testWorkspaceID), ListInput{
		Cursor: result.NextCursor,
	}); err != nil || repository.params.AfterSequence != 12 {
		t.Fatalf("List(checkpoint) error/params = %v/%+v", err, repository.params)
	}
}

func TestListRejectsInvalidAuthorizationCursorAndRepositoryState(t *testing.T) {
	repository := &fakeRepository{page: RepositoryPage{HighWatermark: 0}}
	service := NewService(repository)
	for name, principal := range map[string]auth.Principal{
		"scope":     {WorkspaceID: testWorkspaceID},
		"workspace": {WorkspaceID: "not-a-uuid", Scopes: []string{auth.ScopeAssetsRead}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.List(context.Background(), principal, ListInput{}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("List() error = %v", err)
			}
		})
	}
	for _, input := range []ListInput{
		{Limit: -1}, {Limit: maxListLimit + 1}, {Cursor: "not-base64"},
	} {
		if _, err := service.List(context.Background(), readPrincipal(testWorkspaceID), input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("List(%+v) error = %v", input, err)
		}
	}
	cursor, err := encodeCursor(testWorkspaceID, 5)
	if err != nil {
		t.Fatal(err)
	}
	repository.page = RepositoryPage{HighWatermark: 4}
	if _, err := service.List(context.Background(), readPrincipal(testWorkspaceID), ListInput{Cursor: cursor}); err == nil {
		t.Fatal("List() accepted regressed repository watermark")
	}
}

func readPrincipal(workspaceID string) auth.Principal {
	return auth.Principal{WorkspaceID: workspaceID, Scopes: []string{auth.ScopeAssetsRead}}
}

type fakeRepository struct {
	page   RepositoryPage
	err    error
	params ListParams
}

func (repository *fakeRepository) List(_ context.Context, params ListParams) (RepositoryPage, error) {
	repository.params = params
	return repository.page, repository.err
}
