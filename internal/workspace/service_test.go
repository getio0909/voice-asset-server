package workspace

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
)

func TestGetIsScopedToAuthenticatedWorkspace(t *testing.T) {
	repository := &fakeRepository{got: Workspace{ID: testWorkspaceID, Name: "Primary", Version: 1}}
	service := NewService(repository)

	result, err := service.Get(context.Background(), ownerPrincipal())
	if err != nil || result.ID != testWorkspaceID || repository.getID != testWorkspaceID {
		t.Fatalf("Get() = (%+v, %v), repository ID = %q", result, err, repository.getID)
	}

	for _, principal := range []auth.Principal{
		{WorkspaceID: testWorkspaceID},
		{WorkspaceID: "not-a-uuid", Scopes: []string{auth.ScopeAdminRead}},
	} {
		if _, err := service.Get(context.Background(), principal); !errors.Is(err, ErrForbidden) {
			t.Fatalf("Get(%+v) error = %v", principal, err)
		}
	}
}

func TestUpdateNormalizesNameAndPassesAuditContext(t *testing.T) {
	updatedAt := time.Date(2026, 7, 17, 15, 40, 0, 0, time.UTC)
	repository := &fakeRepository{updated: Workspace{ID: testWorkspaceID, Name: "Renamed", Version: 2}}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 16))
	service.now = func() time.Time { return updatedAt }

	result, err := service.Update(
		context.Background(), ownerPrincipal(), 1, UpdateInput{Name: "  Renamed  "}, "request-1",
	)
	if err != nil || result.Version != 2 {
		t.Fatalf("Update() = (%+v, %v)", result, err)
	}
	params := repository.updateParams
	if params.WorkspaceID != testWorkspaceID || params.ActorID != testOwnerID || params.Name != "Renamed" ||
		params.ExpectedVersion != 1 || params.RequestID != "request-1" || !params.UpdatedAt.Equal(updatedAt) || params.AuditID == "" {
		t.Fatalf("update params = %+v", params)
	}
}

func TestUpdateValidatesOwnerScopeVersionNameAndRequest(t *testing.T) {
	valid := ownerPrincipal()
	reader := valid
	reader.Role = "admin"
	noWrite := valid
	noWrite.Scopes = []string{auth.ScopeAdminRead}
	cases := []struct {
		name      string
		principal auth.Principal
		version   int64
		input     UpdateInput
		requestID string
		want      error
	}{
		{name: "non-owner", principal: reader, version: 1, input: UpdateInput{Name: "Valid"}, requestID: "request", want: ErrForbidden},
		{name: "missing write", principal: noWrite, version: 1, input: UpdateInput{Name: "Valid"}, requestID: "request", want: ErrForbidden},
		{name: "version", principal: valid, version: 0, input: UpdateInput{Name: "Valid"}, requestID: "request", want: ErrInvalidInput},
		{name: "blank", principal: valid, version: 1, input: UpdateInput{Name: "  "}, requestID: "request", want: ErrInvalidInput},
		{name: "long", principal: valid, version: 1, input: UpdateInput{Name: strings.Repeat("界", 201)}, requestID: "request", want: ErrInvalidInput},
		{name: "control", principal: valid, version: 1, input: UpdateInput{Name: "bad\nname"}, requestID: "request", want: ErrInvalidInput},
		{name: "request", principal: valid, version: 1, input: UpdateInput{Name: "Valid"}, requestID: "", want: ErrInvalidInput},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(&fakeRepository{})
			if _, err := service.Update(context.Background(), test.principal, test.version, test.input, test.requestID); !errors.Is(err, test.want) {
				t.Fatalf("Update() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestServicePreservesRepositoryErrors(t *testing.T) {
	repository := &fakeRepository{getErr: ErrNotFound, updateErr: ErrVersionConflict}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x23}, 16))
	if _, err := service.Get(context.Background(), ownerPrincipal()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v", err)
	}
	if _, err := service.Update(context.Background(), ownerPrincipal(), 1, UpdateInput{Name: "Valid"}, "request"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Update() error = %v", err)
	}
}

func ownerPrincipal() auth.Principal {
	return auth.Principal{
		UserID: testOwnerID, WorkspaceID: testWorkspaceID, Role: "owner", Scopes: auth.AllScopes(),
	}
}

type fakeRepository struct {
	got          Workspace
	getID        string
	getErr       error
	updated      Workspace
	updateParams UpdateParams
	updateErr    error
}

func (repository *fakeRepository) Get(_ context.Context, workspaceID string) (Workspace, error) {
	repository.getID = workspaceID
	return repository.got, repository.getErr
}

func (repository *fakeRepository) Update(_ context.Context, params UpdateParams) (Workspace, error) {
	repository.updateParams = params
	return repository.updated, repository.updateErr
}

var _ Repository = (*fakeRepository)(nil)
