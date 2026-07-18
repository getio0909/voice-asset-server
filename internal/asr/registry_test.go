package asr

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type registryFixtureProvider struct {
	id         string
	transcribe func(context.Context, Input) (Result, error)
}

func (provider *registryFixtureProvider) ID() string { return provider.id }

func (provider *registryFixtureProvider) Capabilities() Capabilities {
	return Capabilities{
		ProviderID: provider.id, Batch: true, Languages: []string{"*"}, Models: []string{"fixture"},
		Formats: []string{"wav"}, SampleRates: []int{16_000}, Timestamps: true, WordTimestamps: true,
		MaxDurationMS: 60_000, MaxFileSizeBytes: 1_024 * 1_024, MaxConcurrency: 4,
	}
}

func (provider *registryFixtureProvider) ValidateProfile(profile Profile) error {
	return ValidateProfileAgainst(profile, provider.Capabilities())
}

func (*registryFixtureProvider) Health(ctx context.Context) error { return ctx.Err() }

func (provider *registryFixtureProvider) Cancel(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return newProviderError(provider.id, "cancel", ErrorUnsupported, "fixture", ErrUnsupportedOperation)
}

func (provider *registryFixtureProvider) Transcribe(ctx context.Context, input Input) (Result, error) {
	return provider.transcribe(ctx, input)
}

func registryFixtureProfile(id, providerID string) Profile {
	return Profile{
		ID: id, ProviderID: providerID, Model: "fixture", Language: "zh-CN",
		SampleRate: 16_000, AudioFormat: "wav", Timestamps: true, WordTimestamps: true,
		Timeout: time.Minute, Retry: RetryPolicy{
			MaxAttempts: 1, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second,
		},
		Concurrency: 1,
	}
}

func registryFixtureResult(t *testing.T) Result {
	t.Helper()
	result, err := NewMockProvider().Transcribe(context.Background(), Input{Language: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func consumeRegistryAudio(ctx context.Context, input Input) error {
	stream, err := input.Audio.Open(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()
	_, err = io.ReadAll(stream)
	return err
}

func TestRegistryRetriesThenFailsOverWithFreshAudioStreams(t *testing.T) {
	success := registryFixtureResult(t)
	var primaryCalls atomic.Int32
	var fallbackCalls atomic.Int32
	var opens atomic.Int32
	input := Input{
		AssetID: "asset-fixture", Language: "zh-CN", DurationMS: 1_000,
		Audio: testAudio([]byte("reopenable-audio"), "wav", 16_000),
	}
	originalOpen := input.Audio.Open
	input.Audio.Open = func(ctx context.Context) (io.ReadCloser, error) {
		opens.Add(1)
		return originalOpen(ctx)
	}
	primary := &registryFixtureProvider{id: "primary", transcribe: func(ctx context.Context, input Input) (Result, error) {
		primaryCalls.Add(1)
		if err := consumeRegistryAudio(ctx, input); err != nil {
			return Result{}, err
		}
		return Result{}, newProviderError("primary", "transcribe", ErrorTransient, "fixture_outage", nil)
	}}
	fallback := &registryFixtureProvider{id: "fallback", transcribe: func(ctx context.Context, input Input) (Result, error) {
		fallbackCalls.Add(1)
		if err := consumeRegistryAudio(ctx, input); err != nil {
			return Result{}, err
		}
		return cloneResult(success), nil
	}}
	primaryProfile := registryFixtureProfile("primary-profile", primary.ID())
	primaryProfile.Retry.MaxAttempts = 2
	fallbackProfile := registryFixtureProfile("fallback-profile", fallback.ID())

	registry := NewRegistry()
	var delays []time.Duration
	registry.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	if err := registry.Register(primaryProfile, primary); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(fallbackProfile, fallback); err != nil {
		t.Fatal(err)
	}
	if err := registry.Activate(primaryProfile.ID, fallbackProfile.ID); err != nil {
		t.Fatal(err)
	}

	result, err := registry.Transcribe(context.Background(), input)
	if err != nil {
		t.Fatalf("Transcribe() error = %v", err)
	}
	if result.ProviderID != fallback.ID() || result.ProfileID != fallbackProfile.ID {
		t.Fatalf("selected provider metadata = %q/%q", result.ProviderID, result.ProfileID)
	}
	if primaryCalls.Load() != 2 || fallbackCalls.Load() != 1 || opens.Load() != 3 {
		t.Fatalf("calls primary=%d fallback=%d opens=%d", primaryCalls.Load(), fallbackCalls.Load(), opens.Load())
	}
	if len(delays) != 1 || delays[0] != 100*time.Millisecond {
		t.Fatalf("retry delays = %v", delays)
	}
}

func TestRegistryDoesNotFailOverInvalidAudio(t *testing.T) {
	var fallbackCalls atomic.Int32
	primary := &registryFixtureProvider{id: "primary", transcribe: func(context.Context, Input) (Result, error) {
		return Result{}, newProviderError("primary", "transcribe", ErrorInvalidAudio, "fixture_audio", nil)
	}}
	fallback := &registryFixtureProvider{id: "fallback", transcribe: func(context.Context, Input) (Result, error) {
		fallbackCalls.Add(1)
		return registryFixtureResult(t), nil
	}}
	registry := NewRegistry()
	if err := registry.Register(registryFixtureProfile("primary-profile", primary.ID()), primary); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(registryFixtureProfile("fallback-profile", fallback.ID()), fallback); err != nil {
		t.Fatal(err)
	}
	if err := registry.Activate("primary-profile", "fallback-profile"); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Transcribe(context.Background(), Input{})
	if ErrorClassOf(err) != ErrorInvalidAudio || fallbackCalls.Load() != 0 {
		t.Fatalf("Transcribe() error = %v, fallback calls = %d", err, fallbackCalls.Load())
	}
}

func TestRegistryActivationAndReplacementAreLive(t *testing.T) {
	success := registryFixtureResult(t)
	provider := func(id string) *registryFixtureProvider {
		return &registryFixtureProvider{id: id, transcribe: func(context.Context, Input) (Result, error) {
			return cloneResult(success), nil
		}}
	}
	first := provider("first")
	second := provider("second")
	registry := NewRegistry()
	firstProfile := registryFixtureProfile("first-profile", first.ID())
	secondProfile := registryFixtureProfile("second-profile", second.ID())
	if err := registry.Register(firstProfile, first); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(secondProfile, second); err != nil {
		t.Fatal(err)
	}
	if err := registry.Activate(firstProfile.ID); err != nil {
		t.Fatal(err)
	}
	result, err := registry.Transcribe(context.Background(), Input{})
	if err != nil || result.ProfileID != firstProfile.ID {
		t.Fatalf("first route result = %+v, error = %v", result, err)
	}
	if err := registry.Activate(secondProfile.ID); err != nil {
		t.Fatal(err)
	}
	result, err = registry.Transcribe(context.Background(), Input{})
	if err != nil || result.ProfileID != secondProfile.ID {
		t.Fatalf("second route result = %+v, error = %v", result, err)
	}

	replacement := provider("second")
	if err := registry.Replace(secondProfile, replacement); err != nil {
		t.Fatal(err)
	}
	if err := registry.Remove(secondProfile.ID); !errors.Is(err, ErrProfileActive) {
		t.Fatalf("Remove(active) error = %v, want ErrProfileActive", err)
	}
}

func TestRegistryEnforcesProfileConcurrency(t *testing.T) {
	success := registryFixtureResult(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	provider := &registryFixtureProvider{id: "limited", transcribe: func(ctx context.Context, _ Input) (Result, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return cloneResult(success), nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}}
	profile := registryFixtureProfile("limited-profile", provider.ID())
	registry := NewRegistry()
	if err := registry.Register(profile, provider); err != nil {
		t.Fatal(err)
	}
	if err := registry.Activate(profile.ID); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := registry.Transcribe(context.Background(), Input{})
		firstDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first transcription did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := registry.Transcribe(ctx, Input{})
	if ErrorClassOf(err) != ErrorTransient {
		t.Fatalf("queued Transcribe() error = %v, want transient deadline", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1 while slot is occupied", calls.Load())
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Transcribe() error = %v", err)
	}
}

func TestRegistryProfileSnapshotsAreIsolatedAndDeterministic(t *testing.T) {
	provider := &registryFixtureProvider{id: "fixture", transcribe: func(context.Context, Input) (Result, error) {
		return Result{}, errors.New("unused")
	}}
	profile := registryFixtureProfile("z-profile", provider.ID())
	profile.VendorExtension = []byte(`{}`)
	registry := NewRegistry()
	if err := registry.Register(profile, provider); err != nil {
		t.Fatal(err)
	}
	profile.VendorExtension[0] = '['
	states := registry.Profiles()
	if len(states) != 1 || string(states[0].Profile.VendorExtension) != `{}` || states[0].Active {
		t.Fatalf("Profiles() = %+v", states)
	}
	states[0].Profile.VendorExtension[0] = '['
	if got := string(registry.Profiles()[0].Profile.VendorExtension); got != `{}` {
		t.Fatalf("caller mutation changed registry snapshot: %q", got)
	}
}
