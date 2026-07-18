package asr

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrProfileAlreadyRegistered = errors.New("ASR profile is already registered")
	ErrProfileNotRegistered     = errors.New("ASR profile is not registered")
	ErrProfileActive            = errors.New("ASR profile is active")
)

// ProfileState is a credential-free registry snapshot suitable for an admin
// API. Profile never contains credentials; vendor extensions are copied so a
// caller cannot mutate the active configuration.
type ProfileState struct {
	Profile      Profile
	Capabilities Capabilities
	Active       bool
}

type registryTarget struct {
	profile  Profile
	provider Provider
	slots    chan struct{}
}

// Registry owns immutable provider/profile pairs and the ordered active route.
// Activate and Replace take effect for the next transcription without a worker
// restart; in-flight calls continue against their captured target snapshot.
type Registry struct {
	mu      sync.RWMutex
	targets map[string]*registryTarget
	active  []string
	sleep   func(context.Context, time.Duration) error
}

var _ Transcriber = (*Registry)(nil)

func NewRegistry() *Registry {
	return &Registry{
		targets: make(map[string]*registryTarget),
		sleep:   waitForRetry,
	}
}

// Register adds a validated profile. Profile IDs are stable routing keys and
// must be unique.
func (registry *Registry) Register(profile Profile, provider Provider) error {
	return registry.set(profile, provider, false)
}

// Replace atomically swaps an existing profile implementation while retaining
// its position in the active route.
func (registry *Registry) Replace(profile Profile, provider Provider) error {
	return registry.set(profile, provider, true)
}

func (registry *Registry) set(profile Profile, provider Provider, replace bool) error {
	if provider == nil || strings.TrimSpace(profile.ID) == "" {
		return fmt.Errorf("%w: profile ID or provider is empty", ErrInvalidProfile)
	}
	if profile.ProviderID != provider.ID() {
		return fmt.Errorf("%w: provider ID does not match adapter", ErrInvalidProfile)
	}
	if err := provider.ValidateProfile(profile); err != nil {
		return err
	}
	profile = cloneProfile(profile)
	target := &registryTarget{
		profile: profile, provider: provider, slots: make(chan struct{}, profile.Concurrency),
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	_, exists := registry.targets[profile.ID]
	if exists && !replace {
		return fmt.Errorf("%w: %s", ErrProfileAlreadyRegistered, profile.ID)
	}
	if !exists && replace {
		return fmt.Errorf("%w: %s", ErrProfileNotRegistered, profile.ID)
	}
	registry.targets[profile.ID] = target
	return nil
}

// Activate validates and atomically installs an ordered primary/fallback route.
func (registry *Registry) Activate(profileIDs ...string) error {
	if len(profileIDs) == 0 {
		return fmt.Errorf("%w: active route is empty", ErrInvalidProfile)
	}
	seen := make(map[string]struct{}, len(profileIDs))
	normalized := make([]string, 0, len(profileIDs))

	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, profileID := range profileIDs {
		profileID = strings.TrimSpace(profileID)
		if _, exists := registry.targets[profileID]; !exists {
			return fmt.Errorf("%w: %s", ErrProfileNotRegistered, profileID)
		}
		if _, duplicate := seen[profileID]; duplicate {
			return fmt.Errorf("%w: active route contains duplicate profile %s", ErrInvalidProfile, profileID)
		}
		seen[profileID] = struct{}{}
		normalized = append(normalized, profileID)
	}
	registry.active = normalized
	return nil
}

// Remove deletes an inactive profile. Active route members must be switched
// away first so a configuration update cannot silently alter failover order.
func (registry *Registry) Remove(profileID string) error {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.targets[profileID]; !exists {
		return fmt.Errorf("%w: %s", ErrProfileNotRegistered, profileID)
	}
	for _, activeID := range registry.active {
		if activeID == profileID {
			return fmt.Errorf("%w: %s", ErrProfileActive, profileID)
		}
	}
	delete(registry.targets, profileID)
	return nil
}

// Profiles returns deterministic, credential-free registry state.
func (registry *Registry) Profiles() []ProfileState {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	active := make(map[string]struct{}, len(registry.active))
	for _, profileID := range registry.active {
		active[profileID] = struct{}{}
	}
	states := make([]ProfileState, 0, len(registry.targets))
	for profileID, target := range registry.targets {
		_, isActive := active[profileID]
		states = append(states, ProfileState{
			Profile: cloneProfile(target.profile), Capabilities: cloneCapabilities(target.provider.Capabilities()), Active: isActive,
		})
	}
	sort.Slice(states, func(left, right int) bool { return states[left].Profile.ID < states[right].Profile.ID })
	return states
}

// Transcribe applies per-profile concurrency limits and retry policies, then
// advances through the active route only for failures safe to fail over.
func (registry *Registry) Transcribe(ctx context.Context, input Input) (Result, error) {
	targets, err := registry.activeTargets()
	if err != nil {
		return Result{}, err
	}
	var lastErr error
	for _, target := range targets {
		result, transcribeErr := registry.transcribeTarget(ctx, target, input)
		if transcribeErr == nil {
			result.ProviderID = target.provider.ID()
			result.ProfileID = target.profile.ID
			return result, nil
		}
		lastErr = transcribeErr
		if !canFailover(transcribeErr) {
			return Result{}, transcribeErr
		}
	}
	return Result{}, lastErr
}

func (registry *Registry) activeTargets() ([]*registryTarget, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if len(registry.active) == 0 {
		return nil, newProviderError("asr_registry", "transcribe", ErrorInvalidConfiguration, "no_active_profile", nil)
	}
	targets := make([]*registryTarget, 0, len(registry.active))
	for _, profileID := range registry.active {
		target := registry.targets[profileID]
		if target == nil {
			return nil, newProviderError("asr_registry", "transcribe", ErrorInvalidConfiguration, "missing_active_profile", nil)
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func (registry *Registry) transcribeTarget(
	ctx context.Context,
	target *registryTarget,
	input Input,
) (Result, error) {
	policy := target.profile.Retry
	var lastErr error
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		result, err := callWithConcurrencyLimit(ctx, target, input)
		if err == nil {
			if validateErr := result.Validate(); validateErr != nil {
				err = newProviderError(target.provider.ID(), "transcribe", ErrorRejected, "invalid_result", validateErr)
			} else {
				return result, nil
			}
		}
		lastErr = normalizeProviderFailure(target.provider.ID(), err)
		if !IsRetryable(lastErr) || attempt == policy.MaxAttempts {
			return Result{}, lastErr
		}
		delay := retryDelay(policy, attempt, retryAfter(lastErr))
		if err := registry.sleep(ctx, delay); err != nil {
			return Result{}, contextProviderFailure(target.provider.ID(), err)
		}
	}
	return Result{}, lastErr
}

func callWithConcurrencyLimit(
	ctx context.Context,
	target *registryTarget,
	input Input,
) (Result, error) {
	select {
	case target.slots <- struct{}{}:
		defer func() { <-target.slots }()
	case <-ctx.Done():
		return Result{}, contextProviderFailure(target.provider.ID(), ctx.Err())
	}
	return target.provider.Transcribe(ctx, input)
}

func normalizeProviderFailure(providerID string, err error) error {
	if err == nil || ErrorClassOf(err) != "" {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return contextProviderFailure(providerID, err)
	}
	return newProviderError(providerID, "transcribe", ErrorRejected, "provider_error", err)
}

func contextProviderFailure(providerID string, err error) error {
	class := ErrorCanceled
	code := "context_canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		class = ErrorTransient
		code = "context_deadline"
	}
	return newProviderError(providerID, "transcribe", class, code, err)
}

func canFailover(err error) bool {
	switch ErrorClassOf(err) {
	case ErrorAuthentication, ErrorAuthorization, ErrorRateLimited, ErrorTransient, ErrorRejected, ErrorUnsupported:
		return true
	default:
		return false
	}
}

func retryAfter(err error) time.Duration {
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		return providerError.RetryAfter
	}
	return 0
}

func retryDelay(policy RetryPolicy, failedAttempt int, vendorDelay time.Duration) time.Duration {
	delay := policy.BaseDelay
	for attempt := 1; attempt < failedAttempt && delay < policy.MaxDelay; attempt++ {
		if delay > policy.MaxDelay/2 {
			delay = policy.MaxDelay
			break
		}
		delay *= 2
	}
	if vendorDelay > delay {
		delay = vendorDelay
	}
	if delay > policy.MaxDelay {
		return policy.MaxDelay
	}
	return delay
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cloneProfile(profile Profile) Profile {
	profile.VendorExtension = append([]byte(nil), profile.VendorExtension...)
	return profile
}

func cloneCapabilities(capabilities Capabilities) Capabilities {
	capabilities.Languages = append([]string(nil), capabilities.Languages...)
	capabilities.Models = append([]string(nil), capabilities.Models...)
	capabilities.Formats = append([]string(nil), capabilities.Formats...)
	capabilities.SampleRates = append([]int(nil), capabilities.SampleRates...)
	return capabilities
}
