package waveform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

func TestAccessServiceVerifiesAndOpensWorkspaceWaveform(t *testing.T) {
	content := []byte(strings.Repeat("waveform-png", 4))
	digest := sha256.Sum256(content)
	path := filepath.Join(t.TempDir(), "waveform.png")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	repository := &fakeAccessRepository{stored: Stored{
		ObjectID: "object-1", AssetID: "10000000-0000-4000-8000-000000000001",
		StorageBackend: storage.BackendLocal,
		StorageKey:     "waveform.png", MIMEType: "image/png", Size: int64(len(content)),
		SHA256: hex.EncodeToString(digest[:]), DurationMS: 1000,
	}}
	service := NewAccessService(repository, fakeAccessStore{path: path})
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead}}

	media, err := service.Open(context.Background(), principal, repository.stored.AssetID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer media.Content.Close()
	if media.ObjectID != repository.stored.ObjectID || repository.workspaceID != principal.WorkspaceID {
		t.Fatalf("media/repository = %+v/%q", media, repository.workspaceID)
	}
}

func TestAccessServiceRejectsMissingScopeAndTamperedBytes(t *testing.T) {
	service := NewAccessService(&fakeAccessRepository{}, fakeAccessStore{})
	if _, err := service.Open(context.Background(), auth.Principal{}, "10000000-0000-4000-8000-000000000001"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Open() error = %v, want ErrForbidden", err)
	}

	path := filepath.Join(t.TempDir(), "waveform.png")
	content := []byte(strings.Repeat("tampered-png", 4))
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	repository := &fakeAccessRepository{stored: Stored{
		AssetID: "10000000-0000-4000-8000-000000000001", StorageBackend: storage.BackendLocal,
		StorageKey: "waveform.png",
		MIMEType:   "image/png", Size: int64(len(content)), SHA256: strings.Repeat("a", 64),
	}}
	service = NewAccessService(repository, fakeAccessStore{path: path})
	_, err := service.Open(context.Background(), auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead}}, repository.stored.AssetID)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open() error = %v, want ErrUnavailable", err)
	}
}

type fakeAccessRepository struct {
	stored      Stored
	err         error
	workspaceID string
}

func (repository *fakeAccessRepository) Get(_ context.Context, workspaceID, _ string) (Stored, error) {
	repository.workspaceID = workspaceID
	return repository.stored, repository.err
}

type fakeAccessStore struct {
	path string
}

func (store fakeAccessStore) Open(context.Context, string) (storage.File, error) {
	return os.Open(store.path)
}

func (fakeAccessStore) Backend() storage.Backend { return storage.BackendLocal }
