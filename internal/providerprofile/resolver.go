package providerprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/asr"
)

const defaultMockProfileID = "mock-default"

// Resolver builds a fresh workspace route from enabled profiles. Database
// ordering defines primary/fallback order; constructing per job makes profile
// activation and replacement live without restarting the worker.
type Resolver struct {
	repository Repository
	cipher     SecretCipher
	client     *http.Client
}

func NewResolver(repository Repository, cipher SecretCipher, client *http.Client) *Resolver {
	return &Resolver{repository: repository, cipher: cipher, client: client}
}

func (resolver *Resolver) Resolve(ctx context.Context, workspaceID string) (asr.Transcriber, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(workspaceID) == "" {
		return nil, ErrConfiguration
	}
	profiles, err := resolver.repository.ListEnabledASR(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("%w: load workspace route", ErrConfiguration)
	}
	registry := asr.NewRegistry()
	if len(profiles) == 0 {
		profile := asr.DefaultMockProfile(defaultMockProfileID)
		provider, err := asr.NewConfiguredProvider(profile, nil, resolver.client)
		if err != nil {
			return nil, ErrConfiguration
		}
		if err := registry.Register(profile, provider); err != nil {
			return nil, ErrConfiguration
		}
		if err := registry.Activate(profile.ID); err != nil {
			return nil, ErrConfiguration
		}
		return registry, nil
	}

	active := make([]string, 0, len(profiles))
	for _, stored := range profiles {
		profile, err := stored.Config.ASRProfile(stored.ID, stored.ProviderID)
		if err != nil {
			return nil, profileConfigurationError(stored.ID)
		}
		var secret json.RawMessage
		if stored.SecretConfigured {
			if resolver.cipher == nil || len(stored.SecretCiphertext) == 0 {
				return nil, profileConfigurationError(stored.ID)
			}
			secret, err = resolver.cipher.Open(
				stored.SecretCiphertext,
				profileAssociatedData(stored.WorkspaceID, stored.ID, stored.ProviderID),
			)
			if err != nil {
				return nil, profileConfigurationError(stored.ID)
			}
		}
		provider, err := asr.NewConfiguredProvider(profile, secret, resolver.client)
		clear(secret)
		if err != nil {
			return nil, profileConfigurationError(stored.ID)
		}
		if err := registry.Register(profile, provider); err != nil {
			return nil, profileConfigurationError(stored.ID)
		}
		active = append(active, profile.ID)
	}
	if err := registry.Activate(active...); err != nil {
		return nil, ErrConfiguration
	}
	return registry, nil
}

func profileConfigurationError(profileID string) error {
	return fmt.Errorf("%w: profile %s", ErrConfiguration, profileID)
}
