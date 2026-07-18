package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

var (
	ErrAudioForbidden = errors.New("audio access forbidden")
	ErrAudioNotFound  = errors.New("audio not found")
	ErrAudioStorage   = errors.New("audio storage failure")
)

// Original is the private storage mapping for an asset's immutable source.
// It must never be serialized through the public API.
type Original struct {
	ObjectID       string
	AssetID        string
	StorageBackend storage.Backend
	StorageKey     string
	MIMEType       string
	Container      string
	SampleRate     int
	Size           int64
	SHA256         string
	DurationMS     int64
}

// Media carries only safe response metadata and an already-open file.
type Media struct {
	ObjectID   string
	AssetID    string
	MIMEType   string
	Size       int64
	SHA256     string
	DurationMS int64
	Content    storage.File
}

type OriginalRepository interface {
	GetOriginal(ctx context.Context, workspaceID, assetID string) (Original, error)
}

type OriginalStore interface {
	Backend() storage.Backend
	Open(context.Context, string) (storage.File, error)
}

type AccessService struct {
	repository OriginalRepository
	store      OriginalStore
}

func NewAccessService(repository OriginalRepository, store OriginalStore) *AccessService {
	return &AccessService{repository: repository, store: store}
}

func (s *AccessService) Open(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
) (Media, error) {
	if !principal.Can(auth.ScopeAudioRead) {
		return Media{}, ErrAudioForbidden
	}
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return Media{}, ErrAudioNotFound
	}
	original, err := s.repository.GetOriginal(ctx, principal.WorkspaceID, assetID)
	if err != nil {
		if errors.Is(err, ErrAudioNotFound) {
			return Media{}, ErrAudioNotFound
		}
		return Media{}, fmt.Errorf("load original audio: %w", err)
	}
	if !original.StorageBackend.Valid() || original.StorageBackend != s.store.Backend() {
		return Media{}, fmt.Errorf("%w: storage backend %q is unavailable", ErrAudioStorage, original.StorageBackend)
	}
	file, err := s.store.Open(ctx, original.StorageKey)
	if err != nil {
		return Media{}, fmt.Errorf("%w: open original", ErrAudioStorage)
	}
	info, err := file.Stat()
	if err != nil || info.IsDir() || info.Size() != original.Size {
		_ = file.Close()
		return Media{}, fmt.Errorf("%w: verify original", ErrAudioStorage)
	}
	hasher := sha256.New()
	if _, err := io.CopyN(hasher, contextReader{ctx: ctx, reader: file}, original.Size); err != nil ||
		hex.EncodeToString(hasher.Sum(nil)) != original.SHA256 {
		_ = file.Close()
		return Media{}, fmt.Errorf("%w: verify original checksum", ErrAudioStorage)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return Media{}, fmt.Errorf("%w: reset original", ErrAudioStorage)
	}
	return Media{
		ObjectID: original.ObjectID, AssetID: original.AssetID, MIMEType: original.MIMEType,
		Size: original.Size, SHA256: original.SHA256, DurationMS: original.DurationMS, Content: file,
	}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(destination []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(destination)
}
