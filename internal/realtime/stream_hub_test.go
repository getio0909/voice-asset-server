package realtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamHubRetainsProviderAcrossNetworkReconnect(t *testing.T) {
	factory := &fakeStreamFactory{}
	hub := NewStreamHub(factory)
	session := hubSession(time.Now().UTC().Add(time.Hour))
	lease, err := hub.Start(session, time.Now().UTC())
	if err != nil || hub.Size() != 1 {
		t.Fatalf("Start() = (%+v, %v), size=%d", lease, err, hub.Size())
	}
	if _, err := hub.Resume(session, time.Now().UTC()); !errors.Is(err, ErrStreamAttached) {
		t.Fatalf("Resume(attached) error = %v", err)
	}
	update, err := lease.Push(context.Background(), ProviderFrame{Sequence: 0, PCM: []byte{0, 0}})
	if err != nil || update == nil || update.Revision != 1 {
		t.Fatalf("Push() = (%+v, %v)", update, err)
	}
	reconnectBy := time.Now().UTC().Add(time.Minute)
	if err := lease.Detach(reconnectBy); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Push(context.Background(), ProviderFrame{Sequence: 1, PCM: []byte{0, 0}}); !errors.Is(err, ErrStaleStreamLease) {
		t.Fatalf("stale Push() error = %v", err)
	}
	resumed, err := hub.Resume(session, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Detach(reconnectBy); !errors.Is(err, ErrStaleStreamLease) {
		t.Fatalf("old lease Detach() error = %v", err)
	}
	if _, err := resumed.Push(context.Background(), ProviderFrame{Sequence: 1, PCM: []byte{0, 0}}); err != nil {
		t.Fatal(err)
	}
	result, err := resumed.Finish(context.Background())
	if err != nil || result.Text != "final" || hub.Size() != 0 || factory.stream.closeCalls.Load() != 1 {
		t.Fatalf("Finish() = (%+v, %v), size=%d closes=%d", result, err, hub.Size(), factory.stream.closeCalls.Load())
	}
}

func TestStreamHubAllowsOnlyOneConcurrentResume(t *testing.T) {
	factory := &fakeStreamFactory{}
	hub := NewStreamHub(factory)
	session := hubSession(time.Now().UTC().Add(time.Hour))
	lease, err := hub.Start(session, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Detach(time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wait sync.WaitGroup
	for range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := hub.Resume(session, time.Now().UTC()); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrStreamAttached) {
				t.Errorf("Resume() error = %v", err)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 {
		t.Fatalf("successful resumes = %d, want 1", successes.Load())
	}
}

func TestStreamHubReapsDetachedAndExpiredStreams(t *testing.T) {
	factory := &fakeStreamFactory{}
	hub := NewStreamHub(factory)
	now := time.Now().UTC()
	session := hubSession(now.Add(time.Hour))
	lease, err := hub.Start(session, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Detach(now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := hub.Reap(now.Add(2 * time.Second)); got != 1 || hub.Size() != 0 || factory.stream.closeCalls.Load() != 1 {
		t.Fatalf("Reap() = %d, size=%d closes=%d", got, hub.Size(), factory.stream.closeCalls.Load())
	}
	if _, err := hub.Resume(session, now.Add(3*time.Second)); !errors.Is(err, ErrStreamUnavailable) {
		t.Fatalf("Resume(reaped) error = %v", err)
	}
}

func hubSession(expiresAt time.Time) Session {
	return Session{ID: testSessionID, State: StateStreaming, ExpiresAt: expiresAt}
}

type fakeStreamFactory struct {
	stream *fakeProviderStream
}

func (factory *fakeStreamFactory) Open(_ context.Context, _ Session) (ProviderStream, error) {
	factory.stream = &fakeProviderStream{}
	return factory.stream, nil
}

type fakeProviderStream struct {
	pushes     atomic.Int64
	closeCalls atomic.Int64
}

func (stream *fakeProviderStream) Push(_ context.Context, _ ProviderFrame) (*TranscriptUpdate, error) {
	revision := stream.pushes.Add(1)
	return &TranscriptUpdate{Revision: revision, Text: "partial"}, nil
}

func (*fakeProviderStream) Finish(context.Context) (ProviderResult, error) {
	return ProviderResult{Text: "final", Language: "en-US", ProviderID: "mock_asr"}, nil
}

func (stream *fakeProviderStream) Close() error {
	stream.closeCalls.Add(1)
	return nil
}
