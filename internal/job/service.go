package job

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

type Repository interface {
	CreateTranscription(ctx context.Context, params CreateTranscriptionParams) (Job, bool, error)
	CreateCorrection(ctx context.Context, params CreateCorrectionParams) (Job, bool, error)
	Get(ctx context.Context, workspaceID, jobID string) (Job, error)
}

func (s *Service) CreateCorrection(
	ctx context.Context,
	principal auth.Principal,
	sourceRevisionID,
	idempotencyKey string,
) (Job, bool, error) {
	if !principal.Can(auth.ScopeCorrectionsWrite) {
		return Job{}, false, ErrForbidden
	}
	sourceRevisionID, ok := identifier.NormalizeUUID(sourceRevisionID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !ok || !validIdempotencyKey(idempotencyKey) {
		return Job{}, false, ErrInvalidInput
	}
	jobID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Job{}, false, err
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Job{}, false, err
	}
	payload, err := json.Marshal(struct {
		SourceRevisionID string `json:"source_revision_id"`
	}{sourceRevisionID})
	if err != nil {
		return Job{}, false, fmt.Errorf("encode correction job payload: %w", err)
	}
	digest := sha256.Sum256([]byte(KindLLMCorrect + "\x00" + sourceRevisionID))
	created, replayed, err := s.repository.CreateCorrection(ctx, CreateCorrectionParams{
		JobID: jobID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		SourceRevisionID: sourceRevisionID, CreatedBy: principal.UserID,
		Kind: KindLLMCorrect, Payload: payload, MaxAttempts: DefaultMaxAttempts,
		IdempotencyKey: idempotencyKey, RequestHash: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		for _, sentinel := range []error{
			ErrNotFound, ErrRevisionNotCorrectable, ErrCorrectionActive, ErrIdempotencyConflict,
		} {
			if errors.Is(err, sentinel) {
				return Job{}, false, sentinel
			}
		}
		return Job{}, false, fmt.Errorf("create correction job: %w", err)
	}
	return created, replayed, nil
}

type Service struct {
	repository Repository
	random     io.Reader
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader}
}

func (s *Service) CreateTranscription(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	idempotencyKey string,
) (Job, bool, error) {
	if !principal.Can(auth.ScopeTranscriptionsWrite) {
		return Job{}, false, ErrForbidden
	}
	assetID = strings.TrimSpace(assetID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if assetID == "" || !validIdempotencyKey(idempotencyKey) {
		return Job{}, false, ErrInvalidInput
	}

	jobID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Job{}, false, err
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Job{}, false, err
	}
	payload, err := json.Marshal(struct {
		AssetID string `json:"asset_id"`
	}{AssetID: assetID})
	if err != nil {
		return Job{}, false, fmt.Errorf("encode job payload: %w", err)
	}
	digest := sha256.Sum256([]byte(KindMockTranscribe + "\x00" + assetID))
	created, replayed, err := s.repository.CreateTranscription(ctx, CreateTranscriptionParams{
		JobID: jobID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		AssetID: assetID, CreatedBy: principal.UserID, Kind: KindMockTranscribe,
		Payload: payload, MaxAttempts: DefaultMaxAttempts,
		IdempotencyKey: idempotencyKey, RequestHash: hex.EncodeToString(digest[:]),
	})
	if err != nil {
		for _, sentinel := range []error{ErrNotFound, ErrAssetNotReady, ErrIdempotencyConflict} {
			if errors.Is(err, sentinel) {
				return Job{}, false, sentinel
			}
		}
		return Job{}, false, fmt.Errorf("create transcription job: %w", err)
	}
	return created, replayed, nil
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, jobID string) (Job, error) {
	if !principal.Can(auth.ScopeTranscriptsRead) {
		return Job{}, ErrForbidden
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return Job{}, ErrNotFound
	}
	result, err := s.repository.Get(ctx, principal.WorkspaceID, jobID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Job{}, ErrNotFound
		}
		return Job{}, fmt.Errorf("get job: %w", err)
	}
	return result, nil
}

func validIdempotencyKey(key string) bool {
	if len(key) < 1 || len(key) > 200 {
		return false
	}
	for _, character := range key {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
