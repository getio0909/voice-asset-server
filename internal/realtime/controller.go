package realtime

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

const (
	DefaultHeartbeatInterval = 15 * time.Second
	disconnectCleanupTimeout = 5 * time.Second
	heartbeatMissLimit       = 3
)

var (
	ErrRealtimeTransport = errors.New("realtime transport ended")
	ErrProviderStream    = errors.New("realtime provider stream failed")
	providerIDPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// EventTransport is the message boundary shared by the protocol controller
// and concrete transports such as an authenticated WebSocket connection.
type EventTransport interface {
	Read(context.Context) ([]byte, error)
	Write(context.Context, []byte) error
}

// Controller coordinates durable recording progress with one retained
// provider stream. It does not own authentication or HTTP upgrade policy.
type Controller struct {
	service           *Service
	hub               *StreamHub
	now               func() time.Time
	heartbeatInterval time.Duration
}

func NewController(service *Service, hub *StreamHub) *Controller {
	return &Controller{
		service: service, hub: hub, now: time.Now,
		heartbeatInterval: DefaultHeartbeatInterval,
	}
}

func (controller *Controller) Serve(
	ctx context.Context,
	principal auth.Principal,
	transport EventTransport,
) error {
	if controller == nil || controller.service == nil || controller.hub == nil ||
		controller.now == nil || controller.heartbeatInterval <= 0 || ctx == nil || transport == nil {
		return ErrRealtimeTransport
	}

	firstPayload, err := controller.readEvent(ctx, transport)
	if err != nil {
		return ErrRealtimeTransport
	}
	first, err := DecodeClientEvent(firstPayload)
	if err != nil {
		if writeErr := controller.writeError(ctx, transport, protocolError(ErrInvalidEvent, Session{})); writeErr != nil {
			return writeErr
		}
		return ErrInvalidEvent
	}

	var session Session
	var lease *StreamLease
	switch first.Type {
	case EventStart:
		started, replayed, startErr := controller.service.Start(ctx, principal, *first.Start)
		if startErr != nil {
			if writeErr := controller.writeError(ctx, transport, protocolError(startErr, Session{})); writeErr != nil {
				return writeErr
			}
			return startErr
		}
		if replayed {
			event := ErrorEvent{
				Type: "error", SessionID: started.ID, Code: "session_exists",
				Message: "Session already exists; reconnect with resume.", Retriable: true,
			}
			if writeErr := controller.writeError(ctx, transport, event); writeErr != nil {
				return writeErr
			}
			return ErrStateConflict
		}
		session = started
		lease, err = controller.hub.Start(session, controller.now().UTC())
		if err != nil {
			controller.failSession(ctx, principal, transport, session, nil, "provider_unavailable")
			return ErrProviderStream
		}
	case EventResume:
		current, getErr := controller.service.Get(ctx, principal, first.Resume.SessionID)
		if getErr != nil {
			if writeErr := controller.writeError(ctx, transport, protocolError(getErr, Session{})); writeErr != nil {
				return writeErr
			}
			return getErr
		}
		if current.State == StateCompleted {
			if first.Resume.LastAcknowledgedSequence > current.AcknowledgedSequence() {
				if writeErr := controller.writeError(ctx, transport, protocolError(ErrSequenceGap, current)); writeErr != nil {
					return writeErr
				}
				return ErrSequenceGap
			}
			return controller.replayCompleted(ctx, transport, current)
		}
		if current.State != StateInterrupted {
			if writeErr := controller.writeError(ctx, transport, protocolError(ErrStateConflict, current)); writeErr != nil {
				return writeErr
			}
			return ErrStateConflict
		}
		lease, err = controller.hub.Resume(current, controller.now().UTC())
		if err != nil {
			controller.failSession(ctx, principal, transport, current, nil, "stream_timeout")
			return ErrProviderStream
		}
		resumed, resumeErr := controller.service.Resume(ctx, principal, *first.Resume)
		if resumeErr != nil {
			controller.releaseResumeLease(lease, current)
			if writeErr := controller.writeError(ctx, transport, protocolError(resumeErr, current)); writeErr != nil {
				return writeErr
			}
			return resumeErr
		}
		session = resumed
	default:
		event := ErrorEvent{
			Type: "error", Code: "start_required",
			Message: "The first event must be start or resume.", Retriable: true,
		}
		if writeErr := controller.writeError(ctx, transport, event); writeErr != nil {
			return writeErr
		}
		return ErrStateConflict
	}

	interruptOnExit := true
	defer func() {
		if interruptOnExit {
			controller.retainForReconnect(principal, session.ID, lease)
		}
	}()

	if err := controller.write(ctx, transport, ReadyEvent{
		Type: "ready", ProtocolVersion: ProtocolVersion, SessionID: session.ID,
		NextSequence: session.NextSequence, MaxFrameBytes: session.maximumFrameBytes(),
		HeartbeatIntervalMS: controller.heartbeatInterval.Milliseconds(), ExpiresAt: session.ExpiresAt,
	}); err != nil {
		return err
	}

	var lastRevision int64
	for {
		payload, readErr := controller.readEvent(ctx, transport)
		if readErr != nil {
			return ErrRealtimeTransport
		}
		event, decodeErr := DecodeClientEvent(payload)
		if decodeErr != nil {
			if err := controller.writeError(ctx, transport, protocolError(ErrInvalidEvent, session)); err != nil {
				return err
			}
			continue
		}
		if event.Type == EventStart || event.Type == EventResume {
			if err := controller.writeError(ctx, transport, protocolError(ErrStateConflict, session)); err != nil {
				return err
			}
			continue
		}
		if clientEventSessionID(event) != session.ID {
			if err := controller.writeError(ctx, transport, protocolError(ErrSessionMismatch, session)); err != nil {
				return err
			}
			continue
		}

		switch event.Type {
		case EventAudio:
			persisted, disposition, acceptErr := controller.service.AcceptAudio(ctx, principal, *event.Audio)
			if acceptErr != nil {
				if latest, latestErr := controller.service.Get(ctx, principal, session.ID); latestErr == nil {
					session = latest
				}
				if err := controller.writeError(ctx, transport, protocolError(acceptErr, session)); err != nil {
					return err
				}
				if errors.Is(acceptErr, ErrSequenceGap) || errors.Is(acceptErr, ErrInvalidEvent) ||
					errors.Is(acceptErr, ErrVersionConflict) {
					continue
				}
				return acceptErr
			}
			session = persisted
			var update *TranscriptUpdate
			if disposition == FrameAccepted {
				pcm, pcmErr := event.Audio.PCM()
				if pcmErr != nil {
					if err := controller.writeError(ctx, transport, protocolError(ErrInvalidEvent, session)); err != nil {
						return err
					}
					continue
				}
				update, err = lease.Push(ctx, ProviderFrame{
					Sequence: event.Audio.Sequence, CapturedAtMS: event.Audio.CapturedAtMS, PCM: pcm,
				})
				if err != nil {
					interruptOnExit = false
					controller.failSession(ctx, principal, transport, session, lease, "provider_unavailable")
					return ErrProviderStream
				}
			}
			if err := controller.write(ctx, transport, AckEvent{
				Type: "ack", SessionID: session.ID,
				AcknowledgedSequence: session.AcknowledgedSequence(), ReceivedBytes: session.ReceivedBytes,
			}); err != nil {
				return err
			}
			if update != nil {
				if !validTranscriptUpdate(*update, lastRevision, *event.Audio, session.FrameDurationMS) {
					interruptOnExit = false
					controller.failSession(ctx, principal, transport, session, lease, "provider_rejected")
					return ErrProviderStream
				}
				lastRevision = update.Revision
				if err := controller.write(ctx, transport, PartialTranscriptEvent{
					Type: "partial_transcript", SessionID: session.ID, Revision: update.Revision,
					Text: update.Text, FinalThroughMS: update.FinalThroughMS,
				}); err != nil {
					return err
				}
			}
		case EventHeartbeat:
			if err := controller.write(ctx, transport, HeartbeatAckEvent{
				Type: "heartbeat_ack", SessionID: session.ID, ServerAt: controller.now().UTC(),
			}); err != nil {
				return err
			}
		case EventFinish:
			finalizing, finishErr := controller.service.BeginFinalization(ctx, principal, *event.Finish)
			if finishErr != nil {
				if err := controller.writeError(ctx, transport, protocolError(finishErr, session)); err != nil {
					return err
				}
				if errors.Is(finishErr, ErrFinalSequence) || errors.Is(finishErr, ErrInvalidEvent) ||
					errors.Is(finishErr, ErrVersionConflict) {
					continue
				}
				return finishErr
			}
			session = finalizing
			result, providerErr := lease.Finish(ctx)
			if providerErr != nil {
				interruptOnExit = false
				controller.failSession(ctx, principal, transport, session, lease, "provider_unavailable")
				return ErrProviderStream
			}
			result, ok := normalizeProviderResult(result)
			if !ok {
				interruptOnExit = false
				controller.failSession(ctx, principal, transport, session, lease, "provider_rejected")
				return ErrProviderStream
			}
			completed, completeErr := controller.service.Complete(ctx, principal, session.ID, result)
			if completeErr != nil {
				interruptOnExit = false
				controller.failSession(ctx, principal, transport, session, lease, "internal_error")
				return completeErr
			}
			session = completed
			interruptOnExit = false
			if err := controller.write(ctx, transport, FinalTranscriptEvent{
				Type: "final_transcript", SessionID: session.ID, Text: result.Text,
				Language: result.Language, ProviderID: result.ProviderID,
			}); err != nil {
				return err
			}
			if err := controller.write(ctx, transport, ClosedEvent{
				Type: "closed", SessionID: session.ID, Reason: "completed",
			}); err != nil {
				return err
			}
			return nil
		}
	}
}

func (controller *Controller) readEvent(
	ctx context.Context,
	transport EventTransport,
) ([]byte, error) {
	readCtx, cancel := context.WithTimeout(
		ctx,
		controller.heartbeatInterval*heartbeatMissLimit,
	)
	defer cancel()
	return transport.Read(readCtx)
}

func (controller *Controller) replayCompleted(
	ctx context.Context,
	transport EventTransport,
	session Session,
) error {
	result, valid := normalizeProviderResult(ProviderResult{
		Text: session.FinalTranscript, Language: session.FinalLanguage,
		ProviderID: session.FinalProviderID,
	})
	if !valid {
		return ErrProviderStream
	}
	if err := controller.write(ctx, transport, ReadyEvent{
		Type: "ready", ProtocolVersion: ProtocolVersion, SessionID: session.ID,
		NextSequence: session.NextSequence, MaxFrameBytes: session.maximumFrameBytes(),
		HeartbeatIntervalMS: controller.heartbeatInterval.Milliseconds(), ExpiresAt: session.ExpiresAt,
	}); err != nil {
		return err
	}
	if err := controller.write(ctx, transport, FinalTranscriptEvent{
		Type: "final_transcript", SessionID: session.ID, Text: result.Text,
		Language: result.Language, ProviderID: result.ProviderID,
	}); err != nil {
		return err
	}
	return controller.write(ctx, transport, ClosedEvent{
		Type: "closed", SessionID: session.ID, Reason: "completed",
	})
}

func (controller *Controller) write(ctx context.Context, transport EventTransport, event any) error {
	encoded, err := EncodeServerEvent(event)
	if err != nil {
		return ErrRealtimeTransport
	}
	if err := transport.Write(ctx, encoded); err != nil {
		return ErrRealtimeTransport
	}
	return nil
}

func (controller *Controller) writeError(
	ctx context.Context,
	transport EventTransport,
	event ErrorEvent,
) error {
	return controller.write(ctx, transport, event)
}

func (controller *Controller) retainForReconnect(
	principal auth.Principal,
	sessionID string,
	lease *StreamLease,
) {
	if lease == nil || sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), disconnectCleanupTimeout)
	defer cancel()
	interrupted, err := controller.service.Interrupt(ctx, principal, sessionID)
	if err != nil || interrupted.ReconnectBy == nil {
		_ = lease.Close()
		return
	}
	if err := lease.Detach(*interrupted.ReconnectBy); err != nil {
		_ = lease.Close()
	}
}

func (controller *Controller) releaseResumeLease(lease *StreamLease, session Session) {
	if lease == nil {
		return
	}
	if session.ReconnectBy != nil && lease.Detach(*session.ReconnectBy) == nil {
		return
	}
	_ = lease.Close()
}

func (controller *Controller) failSession(
	writeCtx context.Context,
	principal auth.Principal,
	transport EventTransport,
	session Session,
	lease *StreamLease,
	errorCode string,
) {
	ctx, cancel := context.WithTimeout(context.Background(), disconnectCleanupTimeout)
	defer cancel()
	_, _ = controller.service.Fail(ctx, principal, session.ID, errorCode)
	if lease != nil {
		_ = lease.Close()
	}
	message := "Realtime transcription provider is unavailable."
	if errorCode == "provider_rejected" {
		message = "Realtime transcription provider returned an invalid result."
	} else if errorCode == "internal_error" {
		message = "Realtime transcription could not be completed."
	} else if errorCode == "stream_timeout" {
		message = "The realtime reconnect window has expired."
	}
	_ = controller.writeError(writeCtx, transport, ErrorEvent{
		Type: "error", SessionID: session.ID, Code: errorCode, Message: message, Retriable: false,
	})
	_ = controller.write(writeCtx, transport, ClosedEvent{
		Type: "closed", SessionID: session.ID, Reason: "failed",
	})
}

func protocolError(err error, session Session) ErrorEvent {
	event := ErrorEvent{
		Type: "error", SessionID: session.ID, Code: "internal_error",
		Message: "Realtime transcription could not continue.", Retriable: false,
	}
	switch {
	case errors.Is(err, ErrInvalidEvent):
		event.Code, event.Message, event.Retriable = "invalid_event", "Invalid realtime event.", true
	case errors.Is(err, ErrForbidden):
		event.Code, event.Message = "forbidden", "Realtime transcription is not allowed."
	case errors.Is(err, ErrNotFound):
		event.Code, event.Message = "session_unavailable", "Session is unavailable."
	case errors.Is(err, ErrIdempotencyConflict):
		event.Code, event.Message = "idempotency_conflict", "The idempotency key conflicts with an existing session."
	case errors.Is(err, ErrSequenceGap):
		expected := session.NextSequence
		event.Code, event.Message, event.Retriable = "sequence_gap", "Audio sequence is out of order.", true
		event.ExpectedSequence = &expected
	case errors.Is(err, ErrFinalSequence):
		event.Code, event.Message, event.Retriable = "final_sequence_mismatch", "Final audio sequence does not match.", true
	case errors.Is(err, ErrFrameSize):
		event.Code, event.Message, event.Retriable = "invalid_frame", "Audio frame size does not match the session format.", true
	case errors.Is(err, ErrSessionMismatch):
		event.Code, event.Message = "session_mismatch", "Event session does not match the active session."
	case errors.Is(err, ErrSessionExpired):
		event.Code, event.Message = "session_expired", "The realtime session has expired."
	case errors.Is(err, ErrStateConflict):
		event.Code, event.Message = "state_conflict", "Event is not valid in the current session state."
	case errors.Is(err, ErrVersionConflict):
		event.Code, event.Message, event.Retriable = "concurrent_update", "Session progress changed concurrently.", true
	}
	return event
}

func clientEventSessionID(event ClientEvent) string {
	switch event.Type {
	case EventAudio:
		return event.Audio.SessionID
	case EventFinish:
		return event.Finish.SessionID
	case EventHeartbeat:
		return event.Heartbeat.SessionID
	default:
		return ""
	}
}

func validTranscriptUpdate(
	update TranscriptUpdate,
	lastRevision int64,
	frame AudioEvent,
	frameDurationMS int,
) bool {
	return update.Revision > lastRevision && update.FinalThroughMS >= 0 &&
		frameDurationMS > 0 && update.FinalThroughMS <= frame.CapturedAtMS+int64(frameDurationMS) &&
		utf8.ValidString(update.Text) &&
		len(update.Text) <= MaxTranscriptBytes
}

func normalizeProviderResult(result ProviderResult) (ProviderResult, bool) {
	result.Text = strings.TrimSpace(result.Text)
	result.Language = strings.TrimSpace(result.Language)
	result.ProviderID = strings.TrimSpace(result.ProviderID)
	valid := result.Text != "" && len(result.Text) <= MaxTranscriptBytes && utf8.ValidString(result.Text) &&
		languageTagPattern.MatchString(result.Language) && providerIDPattern.MatchString(result.ProviderID)
	return result, valid
}
