package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

const (
	notificationWorkspaceID = "10000000-0000-4000-8000-0000000000a1"
	notificationUserID      = "20000000-0000-4000-8000-0000000000a1"
	notificationOtherUserID = "20000000-0000-4000-8000-0000000000a2"
)

func TestListPaginatesAndBindsCursorToSessionUser(t *testing.T) {
	now := time.Date(2026, 7, 18, 8, 30, 0, 0, time.UTC)
	repository := &fakeRepository{page: RepositoryPage{
		Items: []Event{
			{Sequence: 1, ID: "30000000-0000-4000-8000-0000000000a1", Type: TypeJobSucceeded, State: StateSucceeded, OccurredAt: now},
			{Sequence: 2, ID: "30000000-0000-4000-8000-0000000000a2", Type: TypeJobFailed, State: StateFailed, OccurredAt: now},
			{Sequence: 3, ID: "30000000-0000-4000-8000-0000000000a3", Type: TypeJobSucceeded, State: StateSucceeded, OccurredAt: now},
		},
		HighWatermark: 3,
	}}
	service := NewService(repository)
	principal := notificationPrincipal(notificationUserID)

	first, err := service.List(context.Background(), principal, ListInput{Limit: 2})
	if err != nil || len(first.Items) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("List(first) = (%+v, %v)", first, err)
	}
	wantParams := ListParams{
		WorkspaceID: notificationWorkspaceID, RecipientUserID: notificationUserID,
		AfterSequence: 0, Limit: 3,
	}
	if repository.params != wantParams {
		t.Fatalf("repository params = %+v, want %+v", repository.params, wantParams)
	}

	repository.page = RepositoryPage{
		Items:         []Event{{Sequence: 3, ID: "30000000-0000-4000-8000-0000000000a3", Type: TypeJobSucceeded, State: StateSucceeded, OccurredAt: now}},
		HighWatermark: 3,
	}
	second, err := service.List(context.Background(), principal, ListInput{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.HasMore || second.NextCursor == "" {
		t.Fatalf("List(second) = (%+v, %v)", second, err)
	}
	if repository.params.AfterSequence != 2 {
		t.Fatalf("after sequence = %d, want 2", repository.params.AfterSequence)
	}

	if _, err := service.List(context.Background(), notificationPrincipal(notificationOtherUserID), ListInput{
		Limit: 2, Cursor: first.NextCursor,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("List(other user cursor) error = %v", err)
	}
}

func TestListReturnsStableCheckpointForEmptyPage(t *testing.T) {
	repository := &fakeRepository{page: RepositoryPage{HighWatermark: 12}}
	service := NewService(repository)
	result, err := service.List(context.Background(), notificationPrincipal(notificationUserID), ListInput{})
	if err != nil || result.Items == nil || len(result.Items) != 0 || result.HasMore || result.NextCursor == "" {
		t.Fatalf("List(empty) = (%+v, %v)", result, err)
	}
	repository.page = RepositoryPage{HighWatermark: 12}
	if _, err := service.List(context.Background(), notificationPrincipal(notificationUserID), ListInput{
		Cursor: result.NextCursor,
	}); err != nil || repository.params.AfterSequence != 12 {
		t.Fatalf("List(checkpoint) error/params = %v/%+v", err, repository.params)
	}
}

func TestListRejectsNonSessionAuthorizationAndInvalidInput(t *testing.T) {
	service := NewService(&fakeRepository{page: RepositoryPage{}})
	principals := map[string]auth.Principal{
		"api key": {
			WorkspaceID: notificationWorkspaceID, UserID: notificationUserID,
			CredentialType: "api_key", Scopes: []string{auth.ScopeTranscriptsRead},
		},
		"scope": {
			WorkspaceID: notificationWorkspaceID, UserID: notificationUserID,
			CredentialType: "session",
		},
		"workspace": {
			WorkspaceID: "invalid", UserID: notificationUserID,
			CredentialType: "session", Scopes: []string{auth.ScopeTranscriptsRead},
		},
		"user": {
			WorkspaceID: notificationWorkspaceID, UserID: "invalid",
			CredentialType: "session", Scopes: []string{auth.ScopeTranscriptsRead},
		},
	}
	for name, principal := range principals {
		t.Run(name, func(t *testing.T) {
			if _, err := service.List(context.Background(), principal, ListInput{}); !errors.Is(err, ErrForbidden) {
				t.Fatalf("List() error = %v", err)
			}
		})
	}
	for _, input := range []ListInput{{Limit: -1}, {Limit: maxListLimit + 1}, {Cursor: "not-base64"}} {
		if _, err := service.List(context.Background(), notificationPrincipal(notificationUserID), input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("List(%+v) error = %v", input, err)
		}
	}
}

func TestListFailsClosedForInvalidRepositoryPage(t *testing.T) {
	principal := notificationPrincipal(notificationUserID)
	cursor, err := encodeCursor(notificationWorkspaceID, notificationUserID, 5)
	if err != nil {
		t.Fatal(err)
	}
	for name, page := range map[string]RepositoryPage{
		"regressed watermark": {HighWatermark: 4},
		"out of order": {
			HighWatermark: 8,
			Items:         []Event{{Sequence: 7}, {Sequence: 6}},
		},
		"past watermark": {
			HighWatermark: 8,
			Items:         []Event{{Sequence: 9}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			service := NewService(&fakeRepository{page: page})
			if _, err := service.List(context.Background(), principal, ListInput{Cursor: cursor}); err == nil {
				t.Fatal("List() accepted invalid repository page")
			}
		})
	}
}

func notificationPrincipal(userID string) auth.Principal {
	return auth.Principal{
		WorkspaceID: notificationWorkspaceID, UserID: userID,
		CredentialType: "session", Scopes: []string{auth.ScopeTranscriptsRead},
	}
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
