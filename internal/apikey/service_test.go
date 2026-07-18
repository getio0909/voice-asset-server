package apikey

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestCreateReturnsTokenOnceAndStoresOnlyDigest(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{created: APIKey{
		ID: "30000000-0000-4000-8000-000000000001", Name: "MCP reader",
	}}
	service := NewService(repository)
	service.now = func() time.Time { return now }
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x01}, 64))
	principal := auth.Principal{
		UserID: "20000000-0000-4000-8000-000000000001", WorkspaceID: "10000000-0000-4000-8000-000000000001",
		Role: "owner", Scopes: auth.AllScopes(),
	}

	result, err := service.Create(context.Background(), principal, CreateInput{
		Name: " MCP reader ", Scopes: []string{auth.ScopeTranscriptsRead, auth.ScopeAssetsRead},
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}, "request-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(result.Token) != len(auth.APIKeyTokenPrefix)+43 || result.APIKey.ID != repository.created.ID {
		t.Fatalf("Create() = %+v", result)
	}
	digest := sha256.Sum256([]byte(result.Token))
	if repository.createParams.TokenHash != hex.EncodeToString(digest[:]) || repository.createParams.TokenHash == result.Token {
		t.Fatalf("stored token material = %+v", repository.createParams)
	}
	if repository.createParams.TokenPrefix != result.Token[:len(auth.APIKeyTokenPrefix)+8] {
		t.Fatalf("token prefix = %q", repository.createParams.TokenPrefix)
	}
	if !reflect.DeepEqual(repository.createParams.Scopes, []string{auth.ScopeAssetsRead, auth.ScopeTranscriptsRead}) {
		t.Fatalf("scopes = %v", repository.createParams.Scopes)
	}
}

func TestCreateRejectsEscalationDuplicatesAndUnsafeExpiry(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	tests := []CreateInput{
		{Name: "MCP", Scopes: []string{auth.ScopeTranscriptionsWrite}, ExpiresAt: now.Add(time.Hour)},
		{Name: "MCP", Scopes: []string{auth.ScopeAssetsRead, auth.ScopeAssetsRead}, ExpiresAt: now.Add(time.Hour)},
		{Name: "MCP", Scopes: []string{auth.ScopeAssetsRead}, ExpiresAt: now.Add(time.Minute)},
		{Name: "MCP", Scopes: []string{auth.ScopeAssetsRead}, ExpiresAt: now.Add(366 * 24 * time.Hour)},
	}
	for _, input := range tests {
		repository := &fakeRepository{}
		service := NewService(repository)
		service.now = func() time.Time { return now }
		principal := auth.Principal{Scopes: []string{auth.ScopeAdminWrite, auth.ScopeAssetsRead}}
		if _, err := service.Create(context.Background(), principal, input, "request-1"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("Create(%+v) error = %v", input, err)
		}
		if repository.createCalls != 0 {
			t.Fatalf("Create(%+v) repository calls = %d", input, repository.createCalls)
		}
	}
}

func TestListAndRevokeEnforceIndependentAdminScopes(t *testing.T) {
	repository := &fakeRepository{listed: []APIKey{{ID: "key-1"}}, revoked: APIKey{ID: "30000000-0000-4000-8000-000000000001"}}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x02}, 16))
	service.now = func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) }
	reader := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAdminRead}}
	if results, err := service.List(context.Background(), reader); err != nil || len(results) != 1 {
		t.Fatalf("List() = (%v, %v)", results, err)
	}
	if _, err := service.Revoke(context.Background(), reader, repository.revoked.ID, "request-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Revoke(read-only) error = %v", err)
	}
	writer := auth.Principal{
		UserID: "20000000-0000-4000-8000-000000000001", WorkspaceID: "10000000-0000-4000-8000-000000000001",
		Scopes: []string{auth.ScopeAdminWrite},
	}
	if _, err := service.Revoke(context.Background(), writer, repository.revoked.ID, "request-1"); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
}

type fakeRepository struct {
	created      APIKey
	createParams CreateParams
	createErr    error
	createCalls  int
	listed       []APIKey
	listErr      error
	revoked      APIKey
	revokeParams RevokeParams
	revokeErr    error
}

func (repository *fakeRepository) Create(_ context.Context, params CreateParams) (APIKey, error) {
	repository.createCalls++
	repository.createParams = params
	return repository.created, repository.createErr
}

func (repository *fakeRepository) List(context.Context, string) ([]APIKey, error) {
	return repository.listed, repository.listErr
}

func (repository *fakeRepository) Revoke(_ context.Context, params RevokeParams) (APIKey, error) {
	repository.revokeParams = params
	return repository.revoked, repository.revokeErr
}
