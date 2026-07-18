// Package clip creates bounded, immutable audio excerpts for agents and users.
package clip

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	MaxDurationMS = int64(5 * 60 * 1000)
	MaxClipBytes  = int64(16 * 1024 * 1024)
	ClipLifetime  = time.Hour
)

var (
	ErrForbidden    = errors.New("clip access forbidden")
	ErrInvalidInput = errors.New("invalid clip input")
	ErrNotFound     = errors.New("clip not found")
	ErrUnavailable  = errors.New("clip processing unavailable")
)

type CreateInput struct {
	StartMS int64 `json:"start_ms"`
	EndMS   int64 `json:"end_ms"`
}

type Clip struct {
	ID          string    `json:"id"`
	AssetID     string    `json:"asset_id"`
	StartMS     int64     `json:"start_ms"`
	EndMS       int64     `json:"end_ms"`
	DurationMS  int64     `json:"duration_ms"`
	MIMEType    string    `json:"mime_type"`
	FileSize    int64     `json:"file_size"`
	SHA256      string    `json:"sha256"`
	DownloadURL string    `json:"download_url"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type StoredClip struct {
	Clip
	StorageBackend storage.Backend
	StorageKey     string
}

type Media struct {
	ClipID   string
	MIMEType string
	Size     int64
	SHA256   string
	Content  storage.File
}

type CreateParams struct {
	ID, AuditID, WorkspaceID, AssetID, ParentObjectID string
	ActorID, ActorType, CredentialID, RequestID       string
	StorageBackend                                    storage.Backend
	StorageKey, SHA256                                string
	StartMS, EndMS, DurationMS, FileSize              int64
	SampleRate, ChannelCount                          int
	Bitrate                                           int64
	ExpiresAt                                         time.Time
}

type Repository interface {
	Create(context.Context, CreateParams) (Clip, error)
	Get(context.Context, string, string, time.Time) (StoredClip, error)
}

type Source interface {
	Open(context.Context, auth.Principal, string) (audio.Media, error)
}

type ClippedAudio struct {
	Content  io.ReadCloser
	Metadata audio.Metadata
}

type Clipper interface {
	Clip(context.Context, storage.File, int64, int64) (ClippedAudio, error)
}

type Store interface {
	Backend() storage.Backend
	PutImmutable(context.Context, string, string, string, io.Reader, int64) (storage.Object, error)
	DeleteObject(context.Context, string, int64, string) error
	Open(context.Context, string) (storage.File, error)
}

type Service struct {
	repository Repository
	source     Source
	clipper    Clipper
	store      Store
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository, source Source, clipper Clipper, store Store) *Service {
	return &Service{
		repository: repository, source: source, clipper: clipper, store: store,
		random: rand.Reader, now: time.Now,
	}
}

func (service *Service) Create(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	input CreateInput,
	requestID string,
) (Clip, error) {
	if !principal.Can(auth.ScopeAudioRead) || !principal.Can(auth.ScopeMetadataWrite) {
		return Clip{}, ErrForbidden
	}
	assetID, validID := identifier.NormalizeUUID(assetID)
	if !validID || input.StartMS < 0 || input.EndMS <= input.StartMS ||
		input.EndMS-input.StartMS > MaxDurationMS || !validRequestID(requestID) {
		return Clip{}, ErrInvalidInput
	}
	if service.repository == nil || service.source == nil || service.clipper == nil || service.store == nil {
		return Clip{}, ErrUnavailable
	}
	source, err := service.source.Open(ctx, principal, assetID)
	if err != nil {
		if errors.Is(err, audio.ErrAudioForbidden) {
			return Clip{}, ErrForbidden
		}
		if errors.Is(err, audio.ErrAudioNotFound) {
			return Clip{}, ErrNotFound
		}
		return Clip{}, fmt.Errorf("open clip source: %w", err)
	}
	defer source.Content.Close()
	if source.DurationMS > 0 && input.EndMS > source.DurationMS {
		return Clip{}, ErrInvalidInput
	}
	clipID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Clip{}, fmt.Errorf("generate clip identifier: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Clip{}, fmt.Errorf("generate clip audit identifier: %w", err)
	}
	processingContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	clipped, err := service.clipper.Clip(processingContext, source.Content, input.StartMS, input.EndMS)
	if err != nil {
		return Clip{}, fmt.Errorf("%w: create audio excerpt", ErrUnavailable)
	}
	defer clipped.Content.Close()
	object, err := service.store.PutImmutable(
		processingContext, assetID, clipID, storage.ObjectKindClip, clipped.Content, MaxClipBytes,
	)
	if err != nil {
		return Clip{}, fmt.Errorf("store audio clip: %w", err)
	}
	expiresAt := service.now().UTC().Add(ClipLifetime)
	created, err := service.repository.Create(ctx, CreateParams{
		ID: clipID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		AssetID: assetID, ParentObjectID: source.ObjectID,
		ActorID: principal.UserID, ActorType: actorType(principal),
		CredentialID: principal.CredentialID, RequestID: requestID,
		StorageBackend: object.Backend, StorageKey: object.Key, SHA256: object.SHA256,
		StartMS: input.StartMS, EndMS: input.EndMS,
		DurationMS: clipped.Metadata.DurationMS, FileSize: object.Size,
		SampleRate: int(clipped.Metadata.SampleRate), ChannelCount: int(clipped.Metadata.Channels),
		Bitrate: int64(clipped.Metadata.Bitrate), ExpiresAt: expiresAt,
	})
	if err != nil {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		cleanupErr := service.store.DeleteObject(cleanupContext, object.Key, object.Size, object.SHA256)
		cancel()
		if errors.Is(err, ErrNotFound) {
			return Clip{}, errors.Join(ErrNotFound, cleanupErr)
		}
		return Clip{}, errors.Join(fmt.Errorf("persist audio clip: %w", err), cleanupErr)
	}
	created.DownloadURL = "/api/v1/audio-clips/" + created.ID
	return created, nil
}

func (service *Service) Open(
	ctx context.Context,
	principal auth.Principal,
	clipID string,
) (Media, error) {
	if !principal.Can(auth.ScopeAudioRead) {
		return Media{}, ErrForbidden
	}
	clipID, validID := identifier.NormalizeUUID(clipID)
	if !validID {
		return Media{}, ErrNotFound
	}
	if service.repository == nil || service.store == nil {
		return Media{}, ErrUnavailable
	}
	stored, err := service.repository.Get(ctx, principal.WorkspaceID, clipID, service.now().UTC())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Media{}, ErrNotFound
		}
		return Media{}, fmt.Errorf("load audio clip: %w", err)
	}
	if !stored.StorageBackend.Valid() || stored.StorageBackend != service.store.Backend() {
		return Media{}, fmt.Errorf("storage backend %q is unavailable", stored.StorageBackend)
	}
	file, err := service.store.Open(ctx, stored.StorageKey)
	if err != nil {
		return Media{}, fmt.Errorf("open audio clip: %w", err)
	}
	info, err := file.Stat()
	if err != nil || info.IsDir() || info.Size() != stored.FileSize {
		_ = file.Close()
		return Media{}, fmt.Errorf("verify audio clip size")
	}
	hasher := sha256.New()
	if _, err := io.CopyN(hasher, file, stored.FileSize); err != nil || hex.EncodeToString(hasher.Sum(nil)) != stored.SHA256 {
		_ = file.Close()
		return Media{}, fmt.Errorf("verify audio clip checksum")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return Media{}, fmt.Errorf("reset audio clip")
	}
	return Media{ClipID: stored.ID, MIMEType: stored.MIMEType, Size: stored.FileSize, SHA256: stored.SHA256, Content: file}, nil
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 200 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func actorType(principal auth.Principal) string {
	if principal.Role == "agent" || principal.CredentialType == "api_key" {
		return "agent"
	}
	return "user"
}
