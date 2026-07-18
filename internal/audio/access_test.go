package audio

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

func TestAccessServiceScopesOriginalLookupAndOpensStorage(t *testing.T) {
	content := []byte("RIFF-test-audio")
	file := temporaryAudioFile(t, content)
	repository := &fakeOriginalRepository{original: Original{
		AssetID: "asset-1", StorageBackend: storage.BackendLocal, StorageKey: "objects/private/original.wav",
		MIMEType: "audio/wav", Size: 15,
		SHA256: sha256Hex(content),
	}}
	store := &fakeOriginalStore{file: file}
	service := NewAccessService(repository, store)
	principal := auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead},
	}

	media, err := service.Open(context.Background(), principal, " asset-1 ")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = media.Content.Close() })
	if repository.workspaceID != "workspace-1" || repository.assetID != "asset-1" {
		t.Fatalf("repository scope = %q/%q", repository.workspaceID, repository.assetID)
	}
	if store.key != repository.original.StorageKey {
		t.Fatalf("store key = %q", store.key)
	}
	if media.MIMEType != "audio/wav" || media.Size != 15 || media.SHA256 != repository.original.SHA256 {
		t.Fatalf("media = %+v", media)
	}
}

func TestAccessServiceRejectsSameSizeChecksumMismatch(t *testing.T) {
	file := temporaryAudioFile(t, []byte("RIFF-tampered!!"))
	repository := &fakeOriginalRepository{original: Original{
		AssetID: "asset-1", StorageBackend: storage.BackendLocal, StorageKey: "objects/private/original.wav",
		MIMEType: "audio/wav", Size: 15,
		SHA256: sha256Hex([]byte("RIFF-test-audio")),
	}}
	service := NewAccessService(repository, &fakeOriginalStore{file: file})
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead}}

	if _, err := service.Open(context.Background(), principal, "asset-1"); !errors.Is(err, ErrAudioStorage) {
		t.Fatalf("Open() checksum error = %v, want ErrAudioStorage", err)
	}
}

func TestAccessServiceRejectsMissingScopeAndHidesWorkspace(t *testing.T) {
	withoutScope := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsRead}}
	service := NewAccessService(&fakeOriginalRepository{}, &fakeOriginalStore{})
	if _, err := service.Open(context.Background(), withoutScope, "asset-1"); !errors.Is(err, ErrAudioForbidden) {
		t.Fatalf("Open() without scope error = %v, want ErrAudioForbidden", err)
	}

	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead}}
	service = NewAccessService(&fakeOriginalRepository{err: ErrAudioNotFound}, &fakeOriginalStore{})
	if _, err := service.Open(context.Background(), principal, "asset-elsewhere"); !errors.Is(err, ErrAudioNotFound) {
		t.Fatalf("Open() cross-workspace error = %v, want ErrAudioNotFound", err)
	}
	if _, err := service.Open(context.Background(), principal, " "); !errors.Is(err, ErrAudioNotFound) {
		t.Fatalf("Open() empty asset error = %v, want ErrAudioNotFound", err)
	}
}

func TestAccessServiceClassifiesMissingStorageAsInternal(t *testing.T) {
	repository := &fakeOriginalRepository{original: Original{
		AssetID: "asset-1", StorageBackend: storage.BackendLocal,
		StorageKey: "objects/private/original.wav", MIMEType: "audio/wav",
	}}
	service := NewAccessService(repository, &fakeOriginalStore{err: os.ErrNotExist})
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead}}
	if _, err := service.Open(context.Background(), principal, "asset-1"); !errors.Is(err, ErrAudioStorage) {
		t.Fatalf("Open() storage error = %v, want ErrAudioStorage", err)
	}
}

func TestAccessServiceRejectsObjectFromDifferentBackend(t *testing.T) {
	repository := &fakeOriginalRepository{original: Original{
		AssetID: "asset-1", StorageBackend: storage.BackendS3,
		StorageKey: "objects/private/original.wav", MIMEType: "audio/wav",
	}}
	store := &fakeOriginalStore{}
	service := NewAccessService(repository, store)
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead}}

	if _, err := service.Open(context.Background(), principal, "asset-1"); !errors.Is(err, ErrAudioStorage) {
		t.Fatalf("Open() backend error = %v, want ErrAudioStorage", err)
	}
	if store.key != "" {
		t.Fatalf("local store opened mismatched object key %q", store.key)
	}
}

type fakeOriginalRepository struct {
	original    Original
	err         error
	workspaceID string
	assetID     string
}

func (r *fakeOriginalRepository) GetOriginal(_ context.Context, workspaceID, assetID string) (Original, error) {
	r.workspaceID = workspaceID
	r.assetID = assetID
	return r.original, r.err
}

type fakeOriginalStore struct {
	file *os.File
	err  error
	key  string
}

func (s *fakeOriginalStore) Open(_ context.Context, key string) (storage.File, error) {
	s.key = key
	return s.file, s.err
}

func (*fakeOriginalStore) Backend() storage.Backend { return storage.BackendLocal }

func temporaryAudioFile(t *testing.T, content []byte) *os.File {
	t.Helper()
	path := t.TempDir() + string(os.PathSeparator) + "audio.wav"
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write audio fixture: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audio fixture: %v", err)
	}
	return file
}

func sha256Hex(content []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(content))
}
