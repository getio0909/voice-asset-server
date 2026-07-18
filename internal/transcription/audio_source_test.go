package transcription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

type fakeOriginalRepository struct {
	original    audio.Original
	err         error
	workspaceID string
	assetID     string
}

func (repository *fakeOriginalRepository) GetOriginal(
	_ context.Context,
	workspaceID,
	assetID string,
) (audio.Original, error) {
	repository.workspaceID = workspaceID
	repository.assetID = assetID
	return repository.original, repository.err
}

type fileOriginalStore struct{ path string }

func (store fileOriginalStore) Open(context.Context, string) (storage.File, error) {
	return os.Open(store.path)
}

func (fileOriginalStore) Backend() storage.Backend { return storage.BackendLocal }

func TestOriginalAudioSourceReturnsVerifiedReopenableStream(t *testing.T) {
	data := []byte("immutable-audio-fixture")
	filePath := filepath.Join(t.TempDir(), "original.m4a")
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	repository := &fakeOriginalRepository{original: audio.Original{
		AssetID: "asset-1", StorageBackend: storage.BackendLocal,
		StorageKey: "objects/original", MIMEType: "audio/mp4",
		Container: "m4a", SampleRate: 48_000, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
	}}
	source := NewOriginalAudioSource(repository, fileOriginalStore{path: filePath})
	resolved, err := source.Resolve(context.Background(), "workspace-1", "asset-1")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Format != "m4a" || resolved.SampleRate != 48_000 || resolved.SizeBytes != int64(len(data)) ||
		repository.workspaceID != "workspace-1" || repository.assetID != "asset-1" {
		t.Fatalf("Resolve() = %+v", resolved)
	}
	for attempt := 0; attempt < 2; attempt++ {
		stream, err := resolved.Open(context.Background())
		if err != nil {
			t.Fatalf("Open(%d) error = %v", attempt, err)
		}
		got, readErr := io.ReadAll(stream)
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || string(got) != string(data) {
			t.Fatalf("Open(%d) read = %q, read error = %v, close error = %v", attempt, got, readErr, closeErr)
		}
	}
}

func TestOriginalAudioSourceDetectsTamperingAndInvalidMetadata(t *testing.T) {
	data := []byte("immutable-audio-fixture")
	filePath := filepath.Join(t.TempDir(), "original.wav")
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	repository := &fakeOriginalRepository{original: audio.Original{
		StorageBackend: storage.BackendLocal, StorageKey: "objects/original",
		MIMEType: "audio/wav", Container: "wav",
		Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
	}}
	source := NewOriginalAudioSource(repository, fileOriginalStore{path: filePath})
	resolved, err := source.Resolve(context.Background(), "workspace-1", "asset-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte("tampered-audio-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolved.Open(context.Background()); !errors.Is(err, ErrSourceAudio) {
		t.Fatalf("Open(tampered) error = %v", err)
	}

	repository.original.MIMEType = "audio/mpeg"
	if _, err := source.Resolve(context.Background(), "workspace-1", "asset-1"); !errors.Is(err, ErrSourceAudio) {
		t.Fatalf("Resolve(unsupported) error = %v", err)
	}
}
