package llmprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/getio0909/voice-asset-server/internal/llm"
)

const defaultMockProfileID = "mock-llm-default"

// ResolvedProvider pairs the provider with the immutable public profile
// snapshot needed by the correction processor.
type ResolvedProvider struct {
	Provider llm.Provider
	Profile  llm.Profile
}

type Resolver struct {
	repository Repository
	cipher     SecretCipher
	client     *http.Client
}

func NewResolver(repository Repository, cipher SecretCipher, client *http.Client) *Resolver {
	return &Resolver{repository: repository, cipher: cipher, client: client}
}

func (resolver *Resolver) Resolve(ctx context.Context, workspaceID string) (ResolvedProvider, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedProvider{}, err
	}
	if strings.TrimSpace(workspaceID) == "" {
		return ResolvedProvider{}, ErrConfiguration
	}
	profiles, err := resolver.repository.ListEnabled(ctx, workspaceID)
	if err != nil {
		return ResolvedProvider{}, fmt.Errorf("%w: load workspace LLM route", ErrConfiguration)
	}
	if len(profiles) == 0 {
		profile := llm.DefaultMockProfile(defaultMockProfileID)
		provider, err := llm.NewConfiguredProvider(profile, nil, resolver.client)
		if err != nil {
			return ResolvedProvider{}, ErrConfiguration
		}
		return ResolvedProvider{Provider: provider, Profile: profile}, nil
	}
	stored := profiles[0]
	profile, err := stored.Config.ProviderProfile(stored.ID, stored.ProviderID)
	if err != nil {
		return ResolvedProvider{}, profileError(stored.ID)
	}
	var secret json.RawMessage
	if stored.SecretConfigured {
		if resolver.cipher == nil || len(stored.SecretCiphertext) == 0 {
			return ResolvedProvider{}, profileError(stored.ID)
		}
		secret, err = resolver.cipher.Open(stored.SecretCiphertext, associatedData(stored.WorkspaceID, stored.ID, stored.ProviderID))
		if err != nil {
			return ResolvedProvider{}, profileError(stored.ID)
		}
	}
	provider, err := llm.NewConfiguredProvider(profile, secret, resolver.client)
	clear(secret)
	if err != nil {
		return ResolvedProvider{}, profileError(stored.ID)
	}
	return ResolvedProvider{Provider: provider, Profile: profile}, nil
}

func profileError(profileID string) error {
	return fmt.Errorf("%w: profile %s", ErrConfiguration, profileID)
}
