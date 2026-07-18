package artifactreaper

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	testWorkspaceID = "10000000-0000-4000-8000-000000000001"
	testAssetID     = "20000000-0000-4000-8000-000000000002"
	testArtifactID  = "30000000-0000-4000-8000-000000000003"
)

func TestReaperDeletesVerifiedExpiredArtifactAndMetadata(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	store, artifact := storedArtifact(t, now.Add(-time.Minute), []byte("expired clip"))
	repository := &fakeRepository{artifacts: []Artifact{artifact}}
	reaper := testReaper(repository, store, now, 16)

	processed, err := reaper.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = (%t, %v), want processed success", processed, err)
	}
	if len(repository.deleted) != 1 || repository.deleted[0].artifact.ID != artifact.ID {
		t.Fatalf("deleted metadata = %+v", repository.deleted)
	}
	if !identifier.IsUUID(repository.deleted[0].auditID) {
		t.Fatalf("audit ID = %q, want UUID", repository.deleted[0].auditID)
	}
	if file, err := store.Open(context.Background(), artifact.StorageKey); err == nil {
		_ = file.Close()
		t.Fatal("expired artifact remains in storage")
	}
}

func TestReaperContinuesAfterIntegrityFailure(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	store, corrupted := storedArtifact(t, now.Add(-time.Hour), []byte("first artifact"))
	corrupted.SHA256 = strings.Repeat("f", 64)
	secondID := "40000000-0000-4000-8000-000000000004"
	object, err := store.PutImmutable(
		context.Background(), testAssetID, secondID, storage.ObjectKindExport,
		strings.NewReader("second artifact"), 1024,
	)
	if err != nil {
		t.Fatalf("store second artifact: %v", err)
	}
	valid := Artifact{
		ID: secondID, WorkspaceID: testWorkspaceID, Kind: KindTranscriptExport,
		StorageBackend: storage.BackendLocal, StorageKey: object.Key, FileSize: object.Size,
		SHA256: object.SHA256, ExpiresAt: now.Add(-time.Minute),
	}
	repository := &fakeRepository{artifacts: []Artifact{corrupted, valid}}
	reaper := testReaper(repository, store, now, 32)

	processed, err := reaper.RunOnce(context.Background())
	if !processed || !errors.Is(err, storage.ErrObjectConflict) {
		t.Fatalf("RunOnce() = (%t, %v), want integrity error", processed, err)
	}
	if len(repository.deleted) != 1 || repository.deleted[0].artifact.ID != valid.ID {
		t.Fatalf("deleted metadata = %+v, want only valid artifact", repository.deleted)
	}
	file, err := store.Open(context.Background(), corrupted.StorageKey)
	if err != nil {
		t.Fatalf("open integrity-mismatched artifact: %v", err)
	}
	_ = file.Close()
	if file, err := store.Open(context.Background(), valid.StorageKey); err == nil {
		_ = file.Close()
		t.Fatal("valid expired artifact remains in storage")
	}
}

func TestReaperRetriesMetadataAfterDatabaseFailure(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	store, artifact := storedArtifact(t, now.Add(-time.Minute), []byte("retry artifact"))
	repository := &fakeRepository{
		artifacts:    []Artifact{artifact},
		deleteErrors: []error{errors.New("database unavailable"), nil},
	}
	reaper := testReaper(repository, store, now, 32)

	if processed, err := reaper.RunOnce(context.Background()); !processed || err == nil {
		t.Fatalf("first RunOnce() = (%t, %v), want retryable database error", processed, err)
	}
	if processed, err := reaper.RunOnce(context.Background()); !processed || err != nil {
		t.Fatalf("second RunOnce() = (%t, %v), want success", processed, err)
	}
	if len(repository.deleted) != 2 {
		t.Fatalf("metadata deletion attempts = %d, want 2", len(repository.deleted))
	}
}

func TestReaperDefensivelyRejectsUnexpiredCandidate(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	store, artifact := storedArtifact(t, now.Add(time.Minute), []byte("future artifact"))
	repository := &fakeRepository{artifacts: []Artifact{artifact}}
	reaper := testReaper(repository, store, now, 16)

	processed, err := reaper.RunOnce(context.Background())
	if !processed || err == nil || !strings.Contains(err.Error(), "has not expired") {
		t.Fatalf("RunOnce() = (%t, %v), want unexpired error", processed, err)
	}
	if len(repository.deleted) != 0 {
		t.Fatalf("deleted metadata = %+v, want none", repository.deleted)
	}
	file, err := store.Open(context.Background(), artifact.StorageKey)
	if err != nil {
		t.Fatalf("unexpired artifact was removed: %v", err)
	}
	_ = file.Close()
}

func TestReaperIsIdleWithoutExpiredArtifacts(t *testing.T) {
	repository := &fakeRepository{}
	reaper := testReaper(repository, &fakeStore{}, time.Now().UTC(), 16)
	processed, err := reaper.RunOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("RunOnce() = (%t, %v), want idle", processed, err)
	}
}

func storedArtifact(t *testing.T, expiresAt time.Time, content []byte) (*storage.Local, Artifact) {
	t.Helper()
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("create local store: %v", err)
	}
	object, err := store.PutImmutable(
		context.Background(), testAssetID, testArtifactID, storage.ObjectKindClip,
		bytes.NewReader(content), 1024,
	)
	if err != nil {
		t.Fatalf("store artifact: %v", err)
	}
	return store, Artifact{
		ID: testArtifactID, WorkspaceID: testWorkspaceID, Kind: KindAudioClip,
		StorageBackend: storage.BackendLocal, StorageKey: object.Key, FileSize: object.Size,
		SHA256: object.SHA256, ExpiresAt: expiresAt,
	}
}

func testReaper(repository Repository, store Store, now time.Time, randomBytes int) *Reaper {
	reaper := New(repository, store)
	reaper.now = func() time.Time { return now }
	reaper.random = bytes.NewReader(make([]byte, randomBytes))
	return reaper
}

type deleteCall struct {
	artifact Artifact
	auditID  string
	now      time.Time
}

type fakeRepository struct {
	artifacts    []Artifact
	deleted      []deleteCall
	deleteErrors []error
}

func (repository *fakeRepository) ListExpired(context.Context, time.Time, int) ([]Artifact, error) {
	return append([]Artifact(nil), repository.artifacts...), nil
}

func (repository *fakeRepository) DeleteExpired(
	_ context.Context,
	artifact Artifact,
	auditID string,
	now time.Time,
) (bool, error) {
	repository.deleted = append(repository.deleted, deleteCall{artifact, auditID, now})
	if len(repository.deleteErrors) == 0 {
		return true, nil
	}
	err := repository.deleteErrors[0]
	repository.deleteErrors = repository.deleteErrors[1:]
	return err == nil, err
}

type fakeStore struct{}

func (*fakeStore) Backend() storage.Backend                                  { return storage.BackendLocal }
func (*fakeStore) DeleteObject(context.Context, string, int64, string) error { return nil }
