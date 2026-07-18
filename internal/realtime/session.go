package realtime

import (
	"errors"
	"time"
)

const (
	StateStreaming   = "streaming"
	StateInterrupted = "interrupted"
	StateFinalizing  = "finalizing"
	StateCompleted   = "completed"
	StateFailed      = "failed"
	StateExpired     = "expired"

	DefaultSessionTTL      = 2 * time.Hour
	DefaultReconnectWindow = 90 * time.Second
)

var (
	ErrStateConflict   = errors.New("realtime session state conflict")
	ErrSessionMismatch = errors.New("realtime session ID mismatch")
	ErrSequenceGap     = errors.New("realtime audio sequence gap")
	ErrFrameSize       = errors.New("realtime audio frame size is invalid")
	ErrSessionExpired  = errors.New("realtime session expired")
	ErrFinalSequence   = errors.New("realtime final sequence does not match")
)

type Session struct {
	ID                  string
	WorkspaceID         string
	CreatedBy           string
	ClientSessionID     string
	ProviderProfileID   string
	HotwordSetID        string
	IdempotencyKey      string
	Encoding            string
	SampleRateHz        int
	Channels            int
	FrameDurationMS     int
	Language            string
	State               string
	NextSequence        int64
	ReceivedBytes       int64
	LastCapturedAtMS    int64
	FinalTranscript     string
	FinalLanguage       string
	FinalProviderID     string
	ClientArchiveSHA256 string
	CapturedDurationMS  int64
	LastErrorCode       string
	Version             int64
	StartedAt           time.Time
	LastFrameAt         *time.Time
	InterruptedAt       *time.Time
	ReconnectBy         *time.Time
	ExpiresAt           time.Time
	CompletedAt         *time.Time
	UpdatedAt           time.Time
}

type NewSessionParams struct {
	ID, WorkspaceID, CreatedBy string
	Start                      StartEvent
	Now                        time.Time
	TTL                        time.Duration
}

type FrameDisposition string

const (
	FrameAccepted  FrameDisposition = "accepted"
	FrameDuplicate FrameDisposition = "duplicate"
)

func NewSession(params NewSessionParams) (Session, error) {
	if !canonicalUUID(params.ID) || !canonicalUUID(params.WorkspaceID) ||
		!canonicalUUID(params.CreatedBy) || params.Start.validate() != nil {
		return Session{}, ErrInvalidEvent
	}
	now := params.Now.UTC()
	if now.IsZero() {
		return Session{}, ErrInvalidEvent
	}
	ttl := params.TTL
	if ttl == 0 {
		ttl = DefaultSessionTTL
	}
	if ttl <= 0 || ttl > DefaultSessionTTL {
		return Session{}, ErrInvalidEvent
	}
	return Session{
		ID: params.ID, WorkspaceID: params.WorkspaceID, CreatedBy: params.CreatedBy,
		ClientSessionID:   params.Start.ClientSessionID,
		ProviderProfileID: params.Start.ProviderProfileID,
		HotwordSetID:      params.Start.HotwordSetID,
		IdempotencyKey:    params.Start.IdempotencyKey,
		Encoding:          params.Start.Encoding, SampleRateHz: params.Start.SampleRateHz,
		Channels: params.Start.Channels, FrameDurationMS: params.Start.FrameDurationMS,
		Language: params.Start.Language, State: StateStreaming, Version: 1,
		StartedAt: now, ExpiresAt: now.Add(ttl), UpdatedAt: now,
	}, nil
}

func (session Session) ApplyAudio(event AudioEvent, now time.Time) (Session, FrameDisposition, error) {
	if session.State != StateStreaming {
		return session, "", ErrStateConflict
	}
	if event.validate() != nil || now.IsZero() {
		return session, "", ErrInvalidEvent
	}
	if event.SessionID != session.ID {
		return session, "", ErrSessionMismatch
	}
	if !now.UTC().Before(session.ExpiresAt) {
		return session, "", ErrSessionExpired
	}
	if event.Sequence < session.NextSequence {
		return session, FrameDuplicate, nil
	}
	if event.Sequence > session.NextSequence {
		return session, "", ErrSequenceGap
	}
	if session.NextSequence > 0 && event.CapturedAtMS < session.LastCapturedAtMS {
		return session, "", ErrInvalidEvent
	}
	pcm, err := event.PCM()
	if err != nil || len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > session.maximumFrameBytes() {
		return session, "", ErrFrameSize
	}
	updatedAt := now.UTC()
	session.NextSequence++
	session.ReceivedBytes += int64(len(pcm))
	session.LastCapturedAtMS = event.CapturedAtMS
	session.LastFrameAt = &updatedAt
	session.UpdatedAt = updatedAt
	session.Version++
	return session, FrameAccepted, nil
}

func (session Session) Interrupt(now time.Time, reconnectWindow time.Duration) (Session, error) {
	if session.State != StateStreaming {
		return session, ErrStateConflict
	}
	if now.IsZero() {
		return session, ErrInvalidEvent
	}
	if reconnectWindow == 0 {
		reconnectWindow = DefaultReconnectWindow
	}
	if reconnectWindow <= 0 || reconnectWindow > 10*time.Minute {
		return session, ErrInvalidEvent
	}
	interruptedAt := now.UTC()
	if !interruptedAt.Before(session.ExpiresAt) {
		return session, ErrSessionExpired
	}
	reconnectBy := interruptedAt.Add(reconnectWindow)
	if reconnectBy.After(session.ExpiresAt) {
		reconnectBy = session.ExpiresAt
	}
	session.State = StateInterrupted
	session.InterruptedAt = &interruptedAt
	session.ReconnectBy = &reconnectBy
	session.UpdatedAt = interruptedAt
	session.Version++
	return session, nil
}

func (session Session) Resume(event ResumeEvent, now time.Time) (Session, error) {
	if session.State != StateInterrupted {
		return session, ErrStateConflict
	}
	if event.validate() != nil || now.IsZero() {
		return session, ErrInvalidEvent
	}
	if event.SessionID != session.ID {
		return session, ErrSessionMismatch
	}
	// The last acknowledgement may have been lost immediately before the
	// disconnect. A lower value is safe because Ready returns NextSequence and
	// replayed frames are idempotent; a higher value would acknowledge data the
	// server never accepted.
	if event.LastAcknowledgedSequence > session.NextSequence-1 {
		return session, ErrSequenceGap
	}
	current := now.UTC()
	if session.ReconnectBy == nil || current.After(*session.ReconnectBy) || !current.Before(session.ExpiresAt) {
		return session, ErrSessionExpired
	}
	session.State = StateStreaming
	session.InterruptedAt = nil
	session.ReconnectBy = nil
	session.UpdatedAt = current
	session.Version++
	return session, nil
}

func (session Session) BeginFinalization(event FinishEvent, now time.Time) (Session, error) {
	if session.State != StateStreaming && session.State != StateInterrupted {
		return session, ErrStateConflict
	}
	if event.validate() != nil || now.IsZero() {
		return session, ErrInvalidEvent
	}
	if event.SessionID != session.ID {
		return session, ErrSessionMismatch
	}
	if event.FinalSequence != session.NextSequence-1 {
		return session, ErrFinalSequence
	}
	if event.CapturedDurationMS < session.LastCapturedAtMS {
		return session, ErrInvalidEvent
	}
	current := now.UTC()
	if !current.Before(session.ExpiresAt) {
		return session, ErrSessionExpired
	}
	session.State = StateFinalizing
	session.ClientArchiveSHA256 = event.ClientArchiveSHA256
	session.CapturedDurationMS = event.CapturedDurationMS
	session.InterruptedAt = nil
	session.ReconnectBy = nil
	session.UpdatedAt = current
	session.Version++
	return session, nil
}

func (session Session) Complete(result ProviderResult, now time.Time) (Session, error) {
	if session.State != StateFinalizing {
		return session, ErrStateConflict
	}
	if now.IsZero() {
		return session, ErrInvalidEvent
	}
	result, valid := normalizeProviderResult(result)
	if !valid {
		return session, ErrInvalidEvent
	}
	completedAt := now.UTC()
	session.State = StateCompleted
	session.FinalTranscript = result.Text
	session.FinalLanguage = result.Language
	session.FinalProviderID = result.ProviderID
	session.CompletedAt = &completedAt
	session.UpdatedAt = completedAt
	session.Version++
	return session, nil
}

func (session Session) Fail(errorCode string) (Session, error) {
	if terminalState(session.State) {
		return session, ErrStateConflict
	}
	if !safeRealtimeErrorCode(errorCode) {
		return session, ErrInvalidEvent
	}
	session.State = StateFailed
	session.LastErrorCode = errorCode
	session.InterruptedAt = nil
	session.ReconnectBy = nil
	session.Version++
	return session, nil
}

func (session Session) Expire(now time.Time) (Session, error) {
	if terminalState(session.State) || now.UTC().Before(session.ExpiresAt) {
		return session, ErrStateConflict
	}
	session.State = StateExpired
	session.InterruptedAt = nil
	session.ReconnectBy = nil
	session.Version++
	return session, nil
}

func (session Session) AcknowledgedSequence() int64 {
	return session.NextSequence - 1
}

func (session Session) maximumFrameBytes() int {
	bytes := session.SampleRateHz * session.Channels * 2 * session.FrameDurationMS / 1000
	if bytes < 1 || bytes > MaxFrameBytes {
		return MaxFrameBytes
	}
	return bytes
}

func terminalState(state string) bool {
	return state == StateCompleted || state == StateFailed || state == StateExpired
}

func safeRealtimeErrorCode(value string) bool {
	switch value {
	case "client_closed", "internal_error", "provider_rejected", "provider_unavailable", "stream_timeout":
		return true
	default:
		return false
	}
}
