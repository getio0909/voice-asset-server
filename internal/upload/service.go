package upload

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	DefaultPartSize     = 5 * 1024 * 1024
	DefaultSessionTTL   = 24 * time.Hour
	MaxUploadBytes      = 512 * 1024 * 1024
	compensationTimeout = 5 * time.Second
	StateActive         = "active"
	StateAssembling     = "assembling"
	StateCompleted      = "completed"
	StateCancelled      = "cancelled"
	StateFailed         = "failed"
)

var (
	ErrForbidden           = errors.New("forbidden")
	ErrInvalidInput        = errors.New("invalid upload input")
	ErrNotFound            = errors.New("upload session not found")
	ErrIdempotencyConflict = errors.New("idempotency key was used for a different request")
	ErrStateConflict       = errors.New("upload session state conflict")
	ErrExpired             = errors.New("upload session expired")
	ErrInvalidPart         = errors.New("invalid upload part")
	ErrPartTooLarge        = errors.New("upload part exceeds its expected size")
	ErrPartConflict        = errors.New("upload part conflict")
	ErrChecksumMismatch    = errors.New("upload checksum mismatch")
	ErrIncomplete          = errors.New("upload parts are incomplete")
	ErrUnsupportedMedia    = errors.New("uploaded media is unsupported")
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Session struct {
	ID             string     `json:"id"`
	AssetID        string     `json:"asset_id"`
	WorkspaceID    string     `json:"workspace_id"`
	Filename       string     `json:"filename"`
	MIMEType       string     `json:"mime_type"`
	ExpectedSize   int64      `json:"expected_size"`
	ExpectedSHA256 string     `json:"expected_sha256"`
	PartSize       int        `json:"part_size"`
	State          string     `json:"state"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	ErrorCode      *string    `json:"error_code"`
	Parts          []Part     `json:"parts"`
}

type CreateInput struct {
	AssetID   string `json:"asset_id"`
	Filename  string `json:"filename"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type CreateParams struct {
	SessionID      string
	AuditID        string
	WorkspaceID    string
	CreatedBy      string
	AssetID        string
	Filename       string
	MIMEType       string
	ExpectedSize   int64
	ExpectedSHA256 string
	PartSize       int
	IdempotencyKey string
	RequestHash    string
	ExpiresAt      time.Time
}

type Part struct {
	Number     int       `json:"number"`
	SizeBytes  int64     `json:"size_bytes"`
	SHA256     string    `json:"sha256"`
	CreatedAt  time.Time `json:"created_at"`
	StorageKey string    `json:"-"`
}

type RecordPartParams struct {
	WorkspaceID string
	UploadID    string
	Part        Part
}

type OriginalObject struct {
	ID             string
	StorageBackend storage.Backend
	StorageKey     string
	MIMEType       string
	Container      string
	Codec          string
	SampleRate     int
	ChannelCount   int
	Bitrate        int64
	DurationMS     int64
	FileSize       int64
	SHA256         string
}

type FinishParams struct {
	WorkspaceID   string
	UploadID      string
	ActorID       string
	AuditID       string
	WaveformJobID string
	Object        OriginalObject
}

type FailureParams struct {
	WorkspaceID string
	UploadID    string
	ActorID     string
	AuditID     string
	ErrorCode   string
}

type Repository interface {
	Create(ctx context.Context, params CreateParams) (Session, bool, error)
	Get(ctx context.Context, workspaceID, uploadID string) (Session, []Part, error)
	RecordPart(ctx context.Context, params RecordPartParams) (Part, bool, error)
	MarkAssembling(ctx context.Context, workspaceID, uploadID string) error
	Finish(ctx context.Context, params FinishParams) (Session, error)
	ResetToActive(ctx context.Context, workspaceID, uploadID string) error
	MarkFailed(ctx context.Context, params FailureParams) error
}

type PartStore interface {
	PutPart(
		ctx context.Context,
		uploadID string,
		partNumber int,
		source io.Reader,
		options storage.PutPartOptions,
	) (storage.Part, error)
}

type CompletionStore interface {
	Assemble(
		ctx context.Context,
		assetID string,
		uploadID string,
		parts []storage.PartRef,
		options storage.AssembleOptions,
	) (storage.Object, error)
	Open(context.Context, string) (storage.File, error)
	DeleteParts(context.Context, string) error
	DeleteObject(context.Context, string, int64, string) error
}

type Service struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
	store      PartStore
	probeMedia func(audio.ProbeSource, string) (audio.Metadata, error)
}

func NewService(repository Repository, stores ...PartStore) *Service {
	service := &Service{
		repository: repository, random: rand.Reader, now: time.Now, probeMedia: audio.ProbeFile,
	}
	if len(stores) > 0 {
		service.store = stores[0]
	}
	return service
}

func (s *Service) Complete(
	ctx context.Context,
	principal auth.Principal,
	uploadID string,
) (Session, bool, error) {
	if !principal.Can(auth.ScopeAssetsWrite) {
		return Session{}, false, ErrForbidden
	}
	workspaceID := normalizeUUID(principal.WorkspaceID)
	actorID := normalizeUUID(principal.UserID)
	uploadID = normalizeUUID(uploadID)
	completionStore, ok := s.store.(CompletionStore)
	if uploadID == "" || !ok {
		return Session{}, false, ErrInvalidInput
	}
	session, parts, err := s.repository.Get(ctx, workspaceID, uploadID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Session{}, false, ErrNotFound
		}
		return Session{}, false, fmt.Errorf("get upload for completion: %w", err)
	}
	if session.State == StateCompleted {
		if cleanupErr := s.deleteParts(ctx, uploadID); cleanupErr != nil {
			return session, true, cleanupErr
		}
		return session, true, nil
	}
	if session.State != StateActive {
		if session.State == StateFailed || session.State == StateCancelled {
			return Session{}, false, joinCleanupError(ErrStateConflict, s.deleteParts(ctx, uploadID))
		}
		return Session{}, false, ErrStateConflict
	}
	if !session.ExpiresAt.After(s.now().UTC()) {
		return Session{}, false, joinCleanupError(ErrExpired, s.deleteParts(ctx, uploadID))
	}
	refs, err := completionPartRefs(session, parts)
	if err != nil {
		return Session{}, false, err
	}
	if err := s.repository.MarkAssembling(ctx, workspaceID, uploadID); err != nil {
		if errors.Is(err, ErrExpired) {
			return Session{}, false, joinCleanupError(ErrExpired, s.deleteParts(ctx, uploadID))
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrStateConflict) {
			return Session{}, false, err
		}
		return Session{}, false, fmt.Errorf("mark upload assembling: %w", err)
	}
	reset := func(cause error) (Session, bool, error) {
		compensationCtx, cancel := compensationContext(ctx)
		defer cancel()
		if resetErr := s.repository.ResetToActive(compensationCtx, workspaceID, uploadID); resetErr != nil {
			return Session{}, false, errors.Join(cause, fmt.Errorf("reset upload state: %w", resetErr))
		}
		return Session{}, false, cause
	}
	object, err := completionStore.Assemble(ctx, normalizeUUID(session.AssetID), uploadID, refs, storage.AssembleOptions{
		ExpectedSize: session.ExpectedSize, ExpectedSHA256: session.ExpectedSHA256, MaxBytes: MaxUploadBytes,
	})
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrChecksumMismatch):
			return reset(ErrChecksumMismatch)
		case errors.Is(err, storage.ErrSizeMismatch), errors.Is(err, storage.ErrPartsOutOfOrder), errors.Is(err, storage.ErrInvalidKey):
			return reset(ErrIncomplete)
		default:
			return reset(fmt.Errorf("assemble original audio: %w", err))
		}
	}
	file, err := completionStore.Open(ctx, object.Key)
	if err != nil {
		return reset(fmt.Errorf("open assembled audio: %w", err))
	}
	metadata, probeErr := s.probeMedia(file, session.MIMEType)
	closeErr := file.Close()
	if probeErr != nil {
		if errors.Is(probeErr, audio.ErrInvalidWAV) || errors.Is(probeErr, audio.ErrInvalidM4A) {
			cleanupCtx, cleanupCancel := compensationContext(ctx)
			cleanupErr := completionStore.DeleteObject(cleanupCtx, object.Key, object.Size, object.SHA256)
			cleanupCancel()
			failureAuditID, idErr := identifier.NewUUIDFrom(s.random)
			if idErr != nil {
				return reset(errors.Join(idErr, cleanupErr))
			}
			compensationCtx, cancel := compensationContext(ctx)
			failErr := s.repository.MarkFailed(compensationCtx, FailureParams{
				WorkspaceID: workspaceID, UploadID: uploadID,
				ActorID: actorID, AuditID: failureAuditID, ErrorCode: "invalid_audio",
			})
			cancel()
			if failErr != nil {
				return reset(errors.Join(
					ErrUnsupportedMedia, cleanupErr, fmt.Errorf("mark upload failed: %w", failErr),
				))
			}
			return Session{}, false, errors.Join(ErrUnsupportedMedia, cleanupErr, s.deleteParts(ctx, uploadID))
		}
		return reset(fmt.Errorf("probe assembled audio: %w", probeErr))
	}
	if closeErr != nil {
		return reset(fmt.Errorf("close assembled audio: %w", closeErr))
	}
	objectID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return reset(err)
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return reset(err)
	}
	waveformJobID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return reset(err)
	}
	completed, err := s.repository.Finish(ctx, FinishParams{
		WorkspaceID:   workspaceID,
		UploadID:      uploadID,
		ActorID:       actorID,
		AuditID:       auditID,
		WaveformJobID: waveformJobID,
		Object: OriginalObject{
			ID: objectID, StorageBackend: object.Backend,
			StorageKey: object.Key, MIMEType: session.MIMEType,
			Container: metadata.Container, Codec: metadata.Codec,
			SampleRate: int(metadata.SampleRate), ChannelCount: int(metadata.Channels),
			Bitrate: int64(metadata.Bitrate), DurationMS: metadata.DurationMS,
			FileSize: object.Size, SHA256: object.SHA256,
		},
	})
	if err != nil {
		return reset(fmt.Errorf("finish upload: %w", err))
	}
	if cleanupErr := s.deleteParts(ctx, uploadID); cleanupErr != nil {
		return completed, object.Reused, cleanupErr
	}
	return completed, object.Reused, nil
}

func completionPartRefs(session Session, parts []Part) ([]storage.PartRef, error) {
	if session.ExpectedSize <= 0 || session.PartSize <= 0 {
		return nil, ErrIncomplete
	}
	partCount := (session.ExpectedSize + int64(session.PartSize) - 1) / int64(session.PartSize)
	if int64(len(parts)) != partCount {
		return nil, ErrIncomplete
	}
	refs := make([]storage.PartRef, 0, len(parts))
	for index, part := range parts {
		number := index + 1
		expectedSize, ok := expectedPartSize(session.ExpectedSize, int64(session.PartSize), number)
		if !ok || part.Number != number || part.SizeBytes != expectedSize ||
			part.StorageKey == "" || !sha256Pattern.MatchString(part.SHA256) {
			return nil, ErrIncomplete
		}
		refs = append(refs, storage.PartRef{
			Number: part.Number, Key: part.StorageKey, Size: part.SizeBytes, SHA256: part.SHA256,
		})
	}
	return refs, nil
}

func (s *Service) PutPart(
	ctx context.Context,
	principal auth.Principal,
	uploadID string,
	partNumber int,
	expectedSHA256 string,
	source io.Reader,
) (Part, bool, error) {
	if !principal.Can(auth.ScopeAssetsWrite) {
		return Part{}, false, ErrForbidden
	}
	workspaceID := normalizeUUID(principal.WorkspaceID)
	uploadID = normalizeUUID(uploadID)
	expectedSHA256 = strings.ToLower(strings.TrimSpace(expectedSHA256))
	if s.store == nil || uploadID == "" || partNumber <= 0 || source == nil || !sha256Pattern.MatchString(expectedSHA256) {
		return Part{}, false, ErrInvalidPart
	}
	session, _, err := s.repository.Get(ctx, workspaceID, uploadID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Part{}, false, ErrNotFound
		}
		return Part{}, false, fmt.Errorf("get upload session: %w", err)
	}
	if session.State != StateActive {
		if session.State == StateFailed || session.State == StateCancelled || session.State == StateCompleted {
			return Part{}, false, joinCleanupError(ErrStateConflict, s.deleteParts(ctx, uploadID))
		}
		return Part{}, false, ErrStateConflict
	}
	if !session.ExpiresAt.After(s.now().UTC()) {
		return Part{}, false, joinCleanupError(ErrExpired, s.deleteParts(ctx, uploadID))
	}
	expectedSize, ok := expectedPartSize(session.ExpectedSize, int64(session.PartSize), partNumber)
	if !ok {
		return Part{}, false, ErrInvalidPart
	}
	stored, err := s.store.PutPart(ctx, uploadID, partNumber, source, storage.PutPartOptions{
		ExpectedSHA256: expectedSHA256,
		ExpectedSize:   expectedSize,
		MaxBytes:       expectedSize,
	})
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrChecksumMismatch):
			return Part{}, false, ErrChecksumMismatch
		case errors.Is(err, storage.ErrPartConflict):
			return Part{}, false, ErrPartConflict
		case errors.Is(err, storage.ErrTooLarge):
			return Part{}, false, ErrPartTooLarge
		case errors.Is(err, storage.ErrSizeMismatch), errors.Is(err, storage.ErrInvalidArgument):
			return Part{}, false, ErrInvalidPart
		default:
			return Part{}, false, fmt.Errorf("store upload part: %w", err)
		}
	}
	part := Part{
		Number: stored.Number, SizeBytes: stored.Size, SHA256: stored.SHA256, StorageKey: stored.Key,
	}
	recorded, replayed, err := s.repository.RecordPart(ctx, RecordPartParams{
		WorkspaceID: workspaceID, UploadID: uploadID, Part: part,
	})
	if err != nil {
		if errors.Is(err, ErrExpired) {
			return Part{}, false, joinCleanupError(ErrExpired, s.deleteParts(ctx, uploadID))
		}
		if errors.Is(err, ErrStateConflict) || errors.Is(err, ErrPartConflict) || errors.Is(err, ErrNotFound) {
			return Part{}, false, err
		}
		return Part{}, false, fmt.Errorf("record upload part: %w", err)
	}
	return recorded, replayed || stored.Reused, nil
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, uploadID string) (Session, error) {
	if !principal.Can(auth.ScopeAssetsWrite) {
		return Session{}, ErrForbidden
	}
	workspaceID := normalizeUUID(principal.WorkspaceID)
	uploadID = normalizeUUID(uploadID)
	if uploadID == "" {
		return Session{}, ErrNotFound
	}
	session, parts, err := s.repository.Get(ctx, workspaceID, uploadID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("get upload session: %w", err)
	}
	if shouldDeleteParts(session, s.now().UTC()) {
		if cleanupErr := s.deleteParts(ctx, uploadID); cleanupErr != nil {
			return Session{}, cleanupErr
		}
	}
	session.Parts = parts
	return session, nil
}

func expectedPartSize(totalSize, partSize int64, partNumber int) (int64, bool) {
	if totalSize <= 0 || partSize <= 0 || partNumber <= 0 {
		return 0, false
	}
	partCount := (totalSize + partSize - 1) / partSize
	if int64(partNumber) > partCount {
		return 0, false
	}
	if int64(partNumber) < partCount {
		return partSize, true
	}
	return totalSize - (partCount-1)*partSize, true
}

func (s *Service) Create(
	ctx context.Context,
	principal auth.Principal,
	input CreateInput,
	idempotencyKey string,
) (Session, bool, error) {
	if !principal.Can(auth.ScopeAssetsWrite) {
		return Session{}, false, ErrForbidden
	}
	workspaceID := normalizeUUID(principal.WorkspaceID)
	createdBy := normalizeUUID(principal.UserID)
	input.AssetID = normalizeUUID(input.AssetID)
	input.Filename = strings.TrimSpace(input.Filename)
	input.MIMEType = strings.ToLower(strings.TrimSpace(input.MIMEType))
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !validDeclaration(input) || !validIdempotencyKey(idempotencyKey) {
		return Session{}, false, ErrInvalidInput
	}
	sessionID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Session{}, false, err
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Session{}, false, err
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%d\x00%s",
		input.AssetID, input.Filename, input.MIMEType, input.SizeBytes, input.SHA256,
	)))
	result, replayed, err := s.repository.Create(ctx, CreateParams{
		SessionID:      sessionID,
		AuditID:        auditID,
		WorkspaceID:    workspaceID,
		CreatedBy:      createdBy,
		AssetID:        input.AssetID,
		Filename:       input.Filename,
		MIMEType:       input.MIMEType,
		ExpectedSize:   input.SizeBytes,
		ExpectedSHA256: input.SHA256,
		PartSize:       DefaultPartSize,
		IdempotencyKey: idempotencyKey,
		RequestHash:    hex.EncodeToString(digest[:]),
		ExpiresAt:      s.now().UTC().Add(DefaultSessionTTL),
	})
	if err != nil {
		if errors.Is(err, ErrIdempotencyConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrStateConflict) {
			return Session{}, false, err
		}
		return Session{}, false, fmt.Errorf("create upload session: %w", err)
	}
	if replayed && shouldDeleteParts(result, s.now().UTC()) {
		if cleanupErr := s.deleteParts(ctx, normalizeUUID(result.ID)); cleanupErr != nil {
			return result, true, cleanupErr
		}
	}
	return result, replayed, nil
}

func normalizeUUID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func compensationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), compensationTimeout)
}

func (s *Service) deleteParts(ctx context.Context, uploadID string) error {
	cleaner, ok := s.store.(interface {
		DeleteParts(context.Context, string) error
	})
	if !ok {
		return nil
	}
	cleanupCtx, cancel := compensationContext(ctx)
	defer cancel()
	if err := cleaner.DeleteParts(cleanupCtx, uploadID); err != nil {
		return fmt.Errorf("delete upload parts: %w", err)
	}
	return nil
}

func joinCleanupError(cause, cleanupErr error) error {
	if cleanupErr == nil {
		return cause
	}
	return errors.Join(cause, cleanupErr)
}

func shouldDeleteParts(session Session, now time.Time) bool {
	return session.State == StateFailed || session.State == StateCancelled || session.State == StateCompleted ||
		(!session.ExpiresAt.IsZero() && !session.ExpiresAt.After(now))
}

func validDeclaration(input CreateInput) bool {
	filenameLength := utf8.RuneCountInString(input.Filename)
	if !identifier.IsUUID(input.AssetID) || filenameLength < 1 || filenameLength > 255 ||
		strings.ContainsAny(input.Filename, `/\`) || input.SizeBytes < 44 || input.SizeBytes > MaxUploadBytes ||
		(input.MIMEType != "audio/wav" && input.MIMEType != "audio/x-wav" && input.MIMEType != "audio/mp4") ||
		!sha256Pattern.MatchString(input.SHA256) {
		return false
	}
	for _, character := range input.Filename {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
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
