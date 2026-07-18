package realtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrStreamAttached    = errors.New("realtime provider stream is already attached")
	ErrStreamUnavailable = errors.New("realtime provider stream is unavailable")
	ErrStaleStreamLease  = errors.New("realtime provider stream lease is stale")
)

type ProviderFrame struct {
	Sequence     int64
	CapturedAtMS int64
	PCM          []byte
}

type TranscriptUpdate struct {
	Revision       int64
	Text           string
	FinalThroughMS int64
}

type ProviderResult struct {
	Text       string
	Language   string
	ProviderID string
}

type ProviderStream interface {
	Push(context.Context, ProviderFrame) (*TranscriptUpdate, error)
	Finish(context.Context) (ProviderResult, error)
	Close() error
}

type StreamFactory interface {
	Open(context.Context, Session) (ProviderStream, error)
}

type StreamHub struct {
	factory StreamFactory
	mu      sync.Mutex
	streams map[string]*managedStream
}

type managedStream struct {
	mu          sync.Mutex
	stream      ProviderStream
	lifetimeCtx context.Context
	cancel      context.CancelFunc
	expiresAt   time.Time
	reconnectBy time.Time
	lease       uint64
	attached    bool
	closed      bool
}

type StreamLease struct {
	hub       *StreamHub
	sessionID string
	lease     uint64
	entry     *managedStream
}

func NewStreamHub(factory StreamFactory) *StreamHub {
	return &StreamHub{factory: factory, streams: make(map[string]*managedStream)}
}

func (hub *StreamHub) Start(session Session, now time.Time) (*StreamLease, error) {
	if hub == nil || hub.factory == nil || !canonicalUUID(session.ID) ||
		now.IsZero() || session.State != StateStreaming || !now.UTC().Before(session.ExpiresAt) {
		return nil, ErrStreamUnavailable
	}
	hub.mu.Lock()
	_, exists := hub.streams[session.ID]
	hub.mu.Unlock()
	if exists {
		return nil, ErrStreamAttached
	}
	lifetimeCtx, cancel := context.WithTimeout(context.Background(), session.ExpiresAt.Sub(now.UTC()))
	stream, err := hub.factory.Open(lifetimeCtx, session)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open realtime provider stream: %w", err)
	}
	if stream == nil {
		cancel()
		return nil, ErrStreamUnavailable
	}
	entry := &managedStream{
		stream: stream, lifetimeCtx: lifetimeCtx, cancel: cancel,
		expiresAt: session.ExpiresAt, lease: 1, attached: true,
	}
	hub.mu.Lock()
	if _, exists := hub.streams[session.ID]; exists {
		hub.mu.Unlock()
		cancel()
		_ = stream.Close()
		return nil, ErrStreamAttached
	}
	hub.streams[session.ID] = entry
	hub.mu.Unlock()
	return &StreamLease{hub: hub, sessionID: session.ID, lease: 1, entry: entry}, nil
}

func (hub *StreamHub) Resume(session Session, now time.Time) (*StreamLease, error) {
	if hub == nil || now.IsZero() ||
		(session.State != StateInterrupted && session.State != StateStreaming) {
		return nil, ErrStreamUnavailable
	}
	hub.mu.Lock()
	entry := hub.streams[session.ID]
	hub.mu.Unlock()
	if entry == nil {
		return nil, ErrStreamUnavailable
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	current := now.UTC()
	if entry.closed || entry.lifetimeCtx.Err() != nil || !current.Before(entry.expiresAt) ||
		(!entry.reconnectBy.IsZero() && current.After(entry.reconnectBy)) {
		return nil, ErrStreamUnavailable
	}
	if entry.attached {
		return nil, ErrStreamAttached
	}
	entry.lease++
	entry.attached = true
	entry.reconnectBy = time.Time{}
	return &StreamLease{
		hub: hub, sessionID: session.ID, lease: entry.lease, entry: entry,
	}, nil
}

func (lease *StreamLease) Push(ctx context.Context, frame ProviderFrame) (*TranscriptUpdate, error) {
	if lease == nil || lease.entry == nil {
		return nil, ErrStaleStreamLease
	}
	entry := lease.entry
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.closed || !entry.attached || entry.lease != lease.lease {
		return nil, ErrStaleStreamLease
	}
	if err := entry.lifetimeCtx.Err(); err != nil {
		return nil, ErrStreamUnavailable
	}
	return entry.stream.Push(ctx, frame)
}

func (lease *StreamLease) Detach(reconnectBy time.Time) error {
	if lease == nil || lease.entry == nil || reconnectBy.IsZero() {
		return ErrStaleStreamLease
	}
	entry := lease.entry
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.closed || !entry.attached || entry.lease != lease.lease {
		return ErrStaleStreamLease
	}
	if reconnectBy.After(entry.expiresAt) {
		reconnectBy = entry.expiresAt
	}
	entry.attached = false
	entry.reconnectBy = reconnectBy.UTC()
	return nil
}

func (lease *StreamLease) Finish(ctx context.Context) (ProviderResult, error) {
	if lease == nil || lease.entry == nil {
		return ProviderResult{}, ErrStaleStreamLease
	}
	entry := lease.entry
	entry.mu.Lock()
	if entry.closed || !entry.attached || entry.lease != lease.lease {
		entry.mu.Unlock()
		return ProviderResult{}, ErrStaleStreamLease
	}
	entry.closed = true
	result, finishErr := entry.stream.Finish(ctx)
	closeErr := entry.stream.Close()
	entry.cancel()
	entry.mu.Unlock()
	lease.hub.remove(lease.sessionID, entry)
	if finishErr != nil || closeErr != nil {
		return ProviderResult{}, errors.Join(finishErr, closeErr)
	}
	return result, nil
}

func (lease *StreamLease) Close() error {
	if lease == nil || lease.entry == nil {
		return ErrStaleStreamLease
	}
	entry := lease.entry
	entry.mu.Lock()
	if entry.closed || entry.lease != lease.lease {
		entry.mu.Unlock()
		return ErrStaleStreamLease
	}
	entry.closed = true
	closeErr := entry.stream.Close()
	entry.cancel()
	entry.mu.Unlock()
	lease.hub.remove(lease.sessionID, entry)
	return closeErr
}

func (hub *StreamHub) Reap(now time.Time) int {
	if hub == nil || now.IsZero() {
		return 0
	}
	hub.mu.Lock()
	snapshot := make(map[string]*managedStream, len(hub.streams))
	for sessionID, entry := range hub.streams {
		snapshot[sessionID] = entry
	}
	hub.mu.Unlock()
	reaped := 0
	for sessionID, entry := range snapshot {
		entry.mu.Lock()
		current := now.UTC()
		expired := !current.Before(entry.expiresAt) ||
			(!entry.attached && !entry.reconnectBy.IsZero() && current.After(entry.reconnectBy))
		if !entry.closed && expired {
			entry.closed = true
			_ = entry.stream.Close()
			entry.cancel()
			reaped++
		}
		closed := entry.closed
		entry.mu.Unlock()
		if closed {
			hub.remove(sessionID, entry)
		}
	}
	return reaped
}

func (hub *StreamHub) Size() int {
	if hub == nil {
		return 0
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return len(hub.streams)
}

func (hub *StreamHub) remove(sessionID string, entry *managedStream) {
	hub.mu.Lock()
	if hub.streams[sessionID] == entry {
		delete(hub.streams, sessionID)
	}
	hub.mu.Unlock()
}
