package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBootstrapOwnerHashesPasswordAndNormalizesInput(t *testing.T) {
	repository := &fakeOwnerCreator{}
	service := NewBootstrapService(repository, PasswordHasher{
		Iterations: 1_000,
		Random:     bytes.NewReader(bytes.Repeat([]byte{0x01}, 64)),
	})
	service.random = bytes.NewReader(append(
		append(
			bytes.Repeat([]byte{0x02}, 16),
			bytes.Repeat([]byte{0x03}, 16)...,
		),
		bytes.Repeat([]byte{0x04}, 16)...,
	))

	owner, err := service.CreateOwner(context.Background(), OwnerInput{
		Email:         " OWNER@Example.com ",
		Password:      "correct horse battery staple",
		WorkspaceName: " Primary Workspace ",
	})
	if err != nil {
		t.Fatalf("CreateOwner() error = %v", err)
	}
	if repository.owner.Email != "owner@example.com" {
		t.Fatalf("Email = %q, want normalized email", repository.owner.Email)
	}
	if repository.owner.WorkspaceName != "Primary Workspace" {
		t.Fatalf("WorkspaceName = %q", repository.owner.WorkspaceName)
	}
	if repository.owner.PasswordHash == "correct horse battery staple" || !strings.HasPrefix(repository.owner.PasswordHash, "$pbkdf2-sha256$") {
		t.Fatalf("PasswordHash = %q, want encoded hash", repository.owner.PasswordHash)
	}
	if owner.Role != "owner" || owner.UserID == "" || owner.WorkspaceID == "" {
		t.Fatalf("unexpected owner: %+v", owner)
	}
}

func TestBootstrapOwnerRejectsWeakPasswordBeforeWrite(t *testing.T) {
	repository := &fakeOwnerCreator{}
	service := NewBootstrapService(repository, PasswordHasher{Iterations: 1_000})

	_, err := service.CreateOwner(context.Background(), OwnerInput{
		Email: "owner@example.com", Password: "too-short", WorkspaceName: "Primary",
	})
	if !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("CreateOwner() error = %v, want ErrWeakPassword", err)
	}
	if repository.calls != 0 {
		t.Fatalf("CreateOwner repository calls = %d, want 0", repository.calls)
	}
}

type fakeOwnerCreator struct {
	owner NewOwner
	calls int
	err   error
}

func (f *fakeOwnerCreator) CreateOwner(_ context.Context, owner NewOwner) (Principal, error) {
	f.calls++
	f.owner = owner
	return Principal{
		UserID: owner.UserID, WorkspaceID: owner.WorkspaceID,
		Role: "owner", Email: owner.Email,
	}, f.err
}
