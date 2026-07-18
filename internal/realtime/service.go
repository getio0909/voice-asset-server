package realtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

var (
	ErrForbidden           = errors.New("forbidden")
	ErrNotFound            = errors.New("realtime session not found")
	ErrIdempotencyConflict = errors.New("idempotency key was used for a different realtime session")
	ErrVersionConflict     = errors.New("realtime session changed concurrently")
)

const (
	AuditSessionStarted     = "recording_session.started"
	AuditSessionInterrupted = "recording_session.interrupted"
	AuditSessionResumed     = "recording_session.resumed"
	AuditSessionFinalizing  = "recording_session.finalizing"
	AuditSessionCompleted   = "recording_session.completed"
	AuditSessionFailed      = "recording_session.failed"
)

type Audit struct {
	ID        string
	ActorID   string
	ActorType string
	Action    string
	Metadata  json.RawMessage
}

type Repository interface {
	Create(ctx context.Context, session Session, requestHash string, audit Audit) (Session, bool, error)
	Get(ctx context.Context, workspaceID, sessionID string) (Session, error)
	Update(ctx context.Context, expectedVersion int64, session Session, audit *Audit) (Session, error)
}

type Service struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader, now: time.Now}
}

func (service *Service) Start(
	ctx context.Context,
	principal auth.Principal,
	event StartEvent,
) (Session, bool, error) {
	if !principal.Can(auth.ScopeTranscriptionsWrite) {
		return Session{}, false, ErrForbidden
	}
	sessionID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Session{}, false, fmt.Errorf("generate realtime session ID: %w", err)
	}
	audit, err := service.newAudit(principal.UserID, AuditSessionStarted, map[string]any{
		"client_session_id": event.ClientSessionID,
		"encoding":          event.Encoding,
		"sample_rate_hz":    event.SampleRateHz,
		"channels":          event.Channels,
		"language":          event.Language,
	})
	if err != nil {
		return Session{}, false, err
	}
	session, err := NewSession(NewSessionParams{
		ID: sessionID, WorkspaceID: principal.WorkspaceID, CreatedBy: principal.UserID,
		Start: event, Now: service.now().UTC(),
	})
	if err != nil {
		return Session{}, false, ErrInvalidEvent
	}
	requestHash, err := startRequestHash(event)
	if err != nil {
		return Session{}, false, fmt.Errorf("hash realtime start request: %w", err)
	}
	created, replayed, err := service.repository.Create(ctx, session, requestHash, audit)
	if err != nil {
		return Session{}, false, mapRepositoryError("create realtime session", err)
	}
	return created, replayed, nil
}

func (service *Service) Get(
	ctx context.Context,
	principal auth.Principal,
	sessionID string,
) (Session, error) {
	if !principal.Can(auth.ScopeTranscriptionsWrite) {
		return Session{}, ErrForbidden
	}
	if !canonicalUUID(sessionID) {
		return Session{}, ErrNotFound
	}
	result, err := service.repository.Get(ctx, principal.WorkspaceID, sessionID)
	if err != nil {
		return Session{}, mapRepositoryError("get realtime session", err)
	}
	return result, nil
}

func (service *Service) AcceptAudio(
	ctx context.Context,
	principal auth.Principal,
	event AudioEvent,
) (Session, FrameDisposition, error) {
	for attempt := 0; attempt < 3; attempt++ {
		current, err := service.Get(ctx, principal, event.SessionID)
		if err != nil {
			return Session{}, "", err
		}
		updated, disposition, err := current.ApplyAudio(event, service.now().UTC())
		if err != nil {
			return Session{}, "", err
		}
		if disposition == FrameDuplicate {
			return current, disposition, nil
		}
		saved, err := service.repository.Update(ctx, current.Version, updated, nil)
		if err == nil {
			return saved, disposition, nil
		}
		if !errors.Is(err, ErrVersionConflict) {
			return Session{}, "", mapRepositoryError("persist realtime audio progress", err)
		}
	}
	return Session{}, "", ErrVersionConflict
}

func (service *Service) Interrupt(
	ctx context.Context,
	principal auth.Principal,
	sessionID string,
) (Session, error) {
	current, err := service.Get(ctx, principal, sessionID)
	if err != nil {
		return Session{}, err
	}
	updated, err := current.Interrupt(service.now().UTC(), DefaultReconnectWindow)
	if err != nil {
		return Session{}, err
	}
	return service.saveTransition(ctx, principal.UserID, current, updated, AuditSessionInterrupted, map[string]any{
		"acknowledged_sequence": updated.AcknowledgedSequence(),
		"reconnect_by":          updated.ReconnectBy,
	})
}

func (service *Service) Resume(
	ctx context.Context,
	principal auth.Principal,
	event ResumeEvent,
) (Session, error) {
	current, err := service.Get(ctx, principal, event.SessionID)
	if err != nil {
		return Session{}, err
	}
	updated, err := current.Resume(event, service.now().UTC())
	if err != nil {
		return Session{}, err
	}
	return service.saveTransition(ctx, principal.UserID, current, updated, AuditSessionResumed, map[string]any{
		"next_sequence": updated.NextSequence,
	})
}

func (service *Service) BeginFinalization(
	ctx context.Context,
	principal auth.Principal,
	event FinishEvent,
) (Session, error) {
	current, err := service.Get(ctx, principal, event.SessionID)
	if err != nil {
		return Session{}, err
	}
	updated, err := current.BeginFinalization(event, service.now().UTC())
	if err != nil {
		return Session{}, err
	}
	return service.saveTransition(ctx, principal.UserID, current, updated, AuditSessionFinalizing, map[string]any{
		"final_sequence":       event.FinalSequence,
		"captured_duration_ms": event.CapturedDurationMS,
		"received_bytes":       updated.ReceivedBytes,
	})
}

func (service *Service) Complete(
	ctx context.Context,
	principal auth.Principal,
	sessionID string,
	result ProviderResult,
) (Session, error) {
	current, err := service.Get(ctx, principal, sessionID)
	if err != nil {
		return Session{}, err
	}
	updated, err := current.Complete(result, service.now().UTC())
	if err != nil {
		return Session{}, err
	}
	return service.saveTransition(ctx, principal.UserID, current, updated, AuditSessionCompleted, map[string]any{
		"transcript_bytes": len(updated.FinalTranscript),
		"received_bytes":   updated.ReceivedBytes,
		"language":         updated.FinalLanguage,
		"provider_id":      updated.FinalProviderID,
	})
}

func (service *Service) Fail(
	ctx context.Context,
	principal auth.Principal,
	sessionID,
	errorCode string,
) (Session, error) {
	current, err := service.Get(ctx, principal, sessionID)
	if err != nil {
		return Session{}, err
	}
	updated, err := current.Fail(errorCode)
	if err != nil {
		return Session{}, err
	}
	return service.saveTransition(ctx, principal.UserID, current, updated, AuditSessionFailed, map[string]any{
		"error_code": errorCode,
	})
}

func (service *Service) saveTransition(
	ctx context.Context,
	actorID string,
	current,
	updated Session,
	action string,
	metadata map[string]any,
) (Session, error) {
	audit, err := service.newAudit(actorID, action, metadata)
	if err != nil {
		return Session{}, err
	}
	saved, err := service.repository.Update(ctx, current.Version, updated, &audit)
	if err != nil {
		return Session{}, mapRepositoryError("persist realtime transition", err)
	}
	return saved, nil
}

func (service *Service) newAudit(actorID, action string, metadata map[string]any) (Audit, error) {
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Audit{}, fmt.Errorf("generate realtime audit ID: %w", err)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return Audit{}, fmt.Errorf("encode realtime audit metadata: %w", err)
	}
	return Audit{ID: auditID, ActorID: actorID, ActorType: "user", Action: action, Metadata: encoded}, nil
}

func startRequestHash(event StartEvent) (string, error) {
	canonical := struct {
		ProtocolVersion, ClientSessionID, Encoding, Language string
		SampleRateHz, Channels, FrameDurationMS              int
		ProviderProfileID, HotwordSetID                      string
	}{
		event.ProtocolVersion, event.ClientSessionID, event.Encoding, event.Language,
		event.SampleRateHz, event.Channels, event.FrameDurationMS,
		event.ProviderProfileID, event.HotwordSetID,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func mapRepositoryError(operation string, err error) error {
	for _, sentinel := range []error{ErrNotFound, ErrIdempotencyConflict, ErrVersionConflict, ErrInvalidEvent} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
