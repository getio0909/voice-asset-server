// Package artifactreaper removes expired, reproducible Agent artifacts while
// preserving source media, transcript revisions, and immutable audit history.
package artifactreaper

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	KindAudioClip        = "audio_clip"
	KindTranscriptExport = "transcript_export"
	DefaultBatchSize     = 25
	MaxBatchSize         = 100
)

// Artifact contains the exact immutable object metadata required for safe
// deletion. Storage bytes are removed only when size and SHA-256 still match.
type Artifact struct {
	ID, WorkspaceID, Kind, StorageKey, SHA256 string
	StorageBackend                            storage.Backend
	FileSize                                  int64
	ExpiresAt                                 time.Time
}

type Repository interface {
	ListExpired(context.Context, time.Time, int) ([]Artifact, error)
	DeleteExpired(context.Context, Artifact, string, time.Time) (bool, error)
}

type Store interface {
	Backend() storage.Backend
	DeleteObject(context.Context, string, int64, string) error
}

type Reaper struct {
	repository Repository
	store      Store
	batchSize  int
	random     io.Reader
	now        func() time.Time
}

func New(repository Repository, store Store) *Reaper {
	return &Reaper{
		repository: repository,
		store:      store,
		batchSize:  DefaultBatchSize,
		random:     rand.Reader,
		now:        time.Now,
	}
}

// RunOnce attempts one bounded batch. File deletion precedes the conditional
// database delete so a database failure remains retryable; an already absent
// file is safe because DeleteObject is idempotent.
func (reaper *Reaper) RunOnce(ctx context.Context) (bool, error) {
	if reaper == nil || reaper.repository == nil || reaper.store == nil ||
		reaper.batchSize < 1 || reaper.batchSize > MaxBatchSize || reaper.random == nil || reaper.now == nil {
		return false, errors.New("artifact reaper is not configured")
	}
	now := reaper.now().UTC()
	artifacts, err := reaper.repository.ListExpired(ctx, now, reaper.batchSize)
	if err != nil {
		return false, fmt.Errorf("list expired artifacts: %w", err)
	}
	if len(artifacts) == 0 {
		return false, nil
	}

	failures := make([]error, 0)
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			failures = append(failures, fmt.Errorf("artifact reaper interrupted: %w", err))
			break
		}
		if err := validateArtifact(artifact, reaper.store.Backend(), now); err != nil {
			failures = append(failures, fmt.Errorf("artifact %q: %w", artifact.ID, err))
			continue
		}
		auditID, err := identifier.NewUUIDFrom(reaper.random)
		if err != nil {
			failures = append(failures, fmt.Errorf("artifact %q audit identifier: %w", artifact.ID, err))
			continue
		}
		if err := reaper.store.DeleteObject(ctx, artifact.StorageKey, artifact.FileSize, artifact.SHA256); err != nil {
			failures = append(failures, fmt.Errorf("artifact %q storage deletion: %w", artifact.ID, err))
			continue
		}
		if _, err := reaper.repository.DeleteExpired(ctx, artifact, auditID, now); err != nil {
			failures = append(failures, fmt.Errorf("artifact %q metadata deletion: %w", artifact.ID, err))
		}
	}
	return true, errors.Join(failures...)
}

func validateArtifact(artifact Artifact, backend storage.Backend, now time.Time) error {
	if !identifier.IsUUID(artifact.ID) || !identifier.IsUUID(artifact.WorkspaceID) {
		return errors.New("database identifiers are invalid")
	}
	if artifact.Kind != KindAudioClip && artifact.Kind != KindTranscriptExport {
		return errors.New("artifact kind is not reaper-managed")
	}
	if !backend.Valid() || artifact.StorageBackend != backend {
		return errors.New("storage backend is not reaper-managed")
	}
	if strings.TrimSpace(artifact.StorageKey) == "" || artifact.FileSize < 0 || !validSHA256(artifact.SHA256) {
		return errors.New("storage metadata is invalid")
	}
	if artifact.ExpiresAt.IsZero() || artifact.ExpiresAt.After(now) {
		return errors.New("artifact has not expired")
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}
