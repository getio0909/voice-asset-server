package waveform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrForbidden   = errors.New("waveform access forbidden")
	ErrNotFound    = errors.New("waveform not found")
	ErrUnavailable = errors.New("waveform unavailable")
)

type Stored struct {
	ObjectID, AssetID, StorageKey, MIMEType, SHA256 string
	StorageBackend                                  storage.Backend
	Size, DurationMS                                int64
}

type Media struct {
	ObjectID, AssetID, MIMEType, SHA256 string
	Size, DurationMS                    int64
	Content                             storage.File
}

type Repository interface {
	Get(context.Context, string, string) (Stored, error)
}

type ObjectStore interface {
	Backend() storage.Backend
	Open(context.Context, string) (storage.File, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (repository *PostgresRepository) Get(ctx context.Context, workspaceID, assetID string) (Stored, error) {
	var result Stored
	err := repository.pool.QueryRow(ctx, `
		SELECT object.id::text, object.asset_id::text, object.storage_backend, object.storage_key,
		       object.mime_type, object.file_size, object.sha256,
		       COALESCE(object.duration_ms, asset.duration_ms, 0)
		FROM asset_objects object
		JOIN assets asset ON asset.id = object.asset_id
		WHERE object.asset_id = $1 AND asset.workspace_id = $2
		  AND object.kind = 'waveform' AND asset.deleted_at IS NULL`, assetID, workspaceID,
	).Scan(&result.ObjectID, &result.AssetID, &result.StorageBackend, &result.StorageKey, &result.MIMEType,
		&result.Size, &result.SHA256, &result.DurationMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stored{}, ErrNotFound
	}
	if err != nil {
		return Stored{}, fmt.Errorf("query waveform: %w", err)
	}
	return result, nil
}

type AccessService struct {
	repository Repository
	store      ObjectStore
}

func NewAccessService(repository Repository, store ObjectStore) *AccessService {
	return &AccessService{repository: repository, store: store}
}

func (service *AccessService) Open(ctx context.Context, principal auth.Principal, assetID string) (Media, error) {
	if !principal.Can(auth.ScopeAudioRead) {
		return Media{}, ErrForbidden
	}
	assetID, valid := identifier.NormalizeUUID(assetID)
	if !valid {
		return Media{}, ErrNotFound
	}
	if service.repository == nil || service.store == nil {
		return Media{}, ErrUnavailable
	}
	stored, err := service.repository.Get(ctx, principal.WorkspaceID, assetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Media{}, ErrNotFound
		}
		return Media{}, fmt.Errorf("load waveform: %w", err)
	}
	if stored.MIMEType != "image/png" || stored.Size <= 24 || stored.Size > MaxPNGBytes {
		return Media{}, ErrUnavailable
	}
	if !stored.StorageBackend.Valid() || stored.StorageBackend != service.store.Backend() {
		return Media{}, fmt.Errorf("%w: storage backend %q is unavailable", ErrUnavailable, stored.StorageBackend)
	}
	file, err := service.store.Open(ctx, stored.StorageKey)
	if err != nil {
		return Media{}, fmt.Errorf("%w: open object", ErrUnavailable)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != stored.Size {
		_ = file.Close()
		return Media{}, fmt.Errorf("%w: verify size", ErrUnavailable)
	}
	digest := sha256.New()
	if _, err := io.CopyN(digest, file, stored.Size); err != nil ||
		hex.EncodeToString(digest.Sum(nil)) != stored.SHA256 {
		_ = file.Close()
		return Media{}, fmt.Errorf("%w: verify checksum", ErrUnavailable)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return Media{}, fmt.Errorf("%w: reset object", ErrUnavailable)
	}
	return Media{
		ObjectID: stored.ObjectID, AssetID: stored.AssetID, MIMEType: stored.MIMEType,
		SHA256: stored.SHA256, Size: stored.Size, DurationMS: stored.DurationMS, Content: file,
	}, nil
}
