package membership

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

const (
	testWorkspaceID = "10000000-0000-4000-8000-000000000001"
	testOwnerID     = "20000000-0000-4000-8000-000000000001"
	testMemberID    = "30000000-0000-4000-8000-000000000001"
)

func TestCreateNormalizesAndHashesWithoutPersistingPassword(t *testing.T) {
	repository := &fakeRepository{created: Member{ID: testMemberID, Email: "member@example.test", Role: "editor"}}
	service := NewService(repository)
	service.hasher = auth.PasswordHasher{Iterations: 1, Random: bytes.NewReader(bytes.Repeat([]byte{0x01}, 16))}
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x02}, 32))

	created, err := service.Create(context.Background(), ownerPrincipal(), CreateInput{
		Email: " Member@Example.Test ", Password: "long-test-password", Role: " Editor ",
	}, "request-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID != testMemberID || repository.createParams.Email != "member@example.test" || repository.createParams.Role != "editor" {
		t.Fatalf("Create() = %+v, params = %+v", created, repository.createParams)
	}
	if repository.createParams.PasswordHash == "long-test-password" || !strings.HasPrefix(repository.createParams.PasswordHash, "$pbkdf2-sha256$") {
		t.Fatalf("password hash = %q", repository.createParams.PasswordHash)
	}
}

func TestWritesRequireOwnerWhileAdminMayList(t *testing.T) {
	repository := &fakeRepository{listed: []Member{{ID: testMemberID}}}
	service := NewService(repository)
	admin := ownerPrincipal()
	admin.Role = "admin"
	if result, err := service.List(context.Background(), admin, ListInput{}); err != nil || len(result.Items) != 1 {
		t.Fatalf("List(admin) = (%+v, %v)", result, err)
	}
	if _, err := service.Create(context.Background(), admin, CreateInput{
		Email: "user@example.test", Password: "long-test-password", Role: "viewer",
	}, "request-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Create(admin) error = %v", err)
	}
	if _, err := service.Update(context.Background(), admin, testMemberID, 1, UpdateInput{
		Role: pointer("viewer"),
	}, "request-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Update(admin) error = %v", err)
	}
}

func TestListCursorIsBoundToWorkspaceAndFilters(t *testing.T) {
	now := time.Date(2026, 7, 17, 14, 0, 0, 0, time.UTC)
	repository := &fakeRepository{listed: []Member{
		{ID: testMemberID, UpdatedAt: now},
		{ID: "30000000-0000-4000-8000-000000000002", UpdatedAt: now.Add(-time.Minute)},
	}}
	service := NewService(repository)
	result, err := service.List(context.Background(), ownerPrincipal(), ListInput{Limit: 1, Role: "viewer"})
	if err != nil || len(result.Items) != 1 || result.NextCursor == nil {
		t.Fatalf("List() = (%+v, %v)", result, err)
	}
	if _, err := service.List(context.Background(), ownerPrincipal(), ListInput{
		Limit: 1, Role: "editor", Cursor: *result.NextCursor,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("List(filter replay) error = %v", err)
	}
}

func TestUpdateValidatesVersionAndMapsSafetyConflicts(t *testing.T) {
	repository := &fakeRepository{updated: Member{ID: testMemberID, Version: 2}}
	service := NewService(repository)
	service.now = func() time.Time { return time.Date(2026, 7, 17, 14, 0, 0, 0, time.UTC) }
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x03}, 64))
	updated, err := service.Update(context.Background(), ownerPrincipal(), testMemberID, 1, UpdateInput{
		Role: pointer("admin"), Status: pointer("active"),
	}, "request-1")
	if err != nil || updated.Version != 2 || repository.updateParams.ExpectedVersion != 1 {
		t.Fatalf("Update() = (%+v, %v), params = %+v", updated, err, repository.updateParams)
	}
	for repositoryErr, expected := range map[error]error{
		ErrNotFound: ErrNotFound, ErrVersionConflict: ErrVersionConflict, ErrLastOwner: ErrLastOwner,
	} {
		repository.updateErr = repositoryErr
		if _, err := service.Update(context.Background(), ownerPrincipal(), testMemberID, 1, UpdateInput{
			Status: pointer("disabled"),
		}, "request-1"); !errors.Is(err, expected) {
			t.Fatalf("Update(%v) error = %v", repositoryErr, err)
		}
	}
}

func TestRejectsInvalidCreateAndEmptyUpdate(t *testing.T) {
	service := NewService(&fakeRepository{})
	tests := []CreateInput{
		{Email: "invalid", Password: "long-test-password", Role: "viewer"},
		{Email: "user@example.test", Password: "short", Role: "viewer"},
		{Email: "user@example.test", Password: "long-test-password", Role: "superuser"},
	}
	for _, input := range tests {
		if _, err := service.Create(context.Background(), ownerPrincipal(), input, "request-1"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("Create(%+v) error = %v", input, err)
		}
	}
	if _, err := service.Update(context.Background(), ownerPrincipal(), testMemberID, 1, UpdateInput{}, "request-1"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Update(empty) error = %v", err)
	}
}

func ownerPrincipal() auth.Principal {
	return auth.Principal{
		UserID: testOwnerID, WorkspaceID: testWorkspaceID, Role: "owner", Scopes: auth.AllScopes(),
	}
}

func pointer(value string) *string { return &value }

type fakeRepository struct {
	created      Member
	createParams CreateParams
	createErr    error
	listed       []Member
	listParams   ListParams
	listErr      error
	updated      Member
	updateParams UpdateParams
	updateErr    error
}

func (repository *fakeRepository) Create(_ context.Context, params CreateParams) (Member, error) {
	repository.createParams = params
	return repository.created, repository.createErr
}

func (repository *fakeRepository) List(_ context.Context, params ListParams) ([]Member, error) {
	repository.listParams = params
	return repository.listed, repository.listErr
}

func (repository *fakeRepository) Update(_ context.Context, params UpdateParams) (Member, error) {
	repository.updateParams = params
	return repository.updated, repository.updateErr
}
