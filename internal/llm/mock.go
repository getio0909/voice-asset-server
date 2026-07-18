package llm

import (
	"context"
	"encoding/json"
	"strings"
)

type MockProvider struct {
	profile Profile
}

func NewMockProvider(profile Profile) (*MockProvider, error) {
	provider := &MockProvider{profile: profile}
	if err := provider.ValidateProfile(profile); err != nil {
		return nil, newProviderError(MockProviderID, "configure", ErrorInvalidConfiguration, "profile", err)
	}
	return provider, nil
}

func (*MockProvider) ID() string { return MockProviderID }

func (*MockProvider) Capabilities() Capabilities {
	return Capabilities{
		ProviderID: MockProviderID, StructuredPatch: true,
		MaxContextTokens: 1_000_000, MaxConcurrency: 128,
	}
}

func (*MockProvider) ValidateProfile(profile Profile) error {
	if profile.ProviderID != MockProviderID {
		return newProviderError(MockProviderID, "configure", ErrorInvalidConfiguration, "provider_id", nil)
	}
	return ValidateProfileDefinition(profile)
}

func (*MockProvider) Health(ctx context.Context) error { return ctx.Err() }

func (provider *MockProvider) Correct(ctx context.Context, request Request) (Proposal, error) {
	if err := ctx.Err(); err != nil {
		return Proposal{}, newProviderError(MockProviderID, "correct", ErrorCanceled, "context", err)
	}
	if err := ValidateRequest(request); err != nil {
		return Proposal{}, newProviderError(MockProviderID, "correct", ErrorRejected, "request", err)
	}
	changes := make([]Change, 0)
	for _, segment := range request.Segments {
		replacement := ApplyGlossary(segment.Text, request.Language, request.Glossary)
		if replacement == segment.Text {
			continue
		}
		changes = append(changes, Change{
			SegmentID: segment.ID, Original: segment.Text, Replacement: replacement,
			Confidence: 1, Reason: "validated glossary substitution",
		})
	}
	raw, err := json.Marshal(struct {
		Changes []Change `json:"changes"`
	}{Changes: changes})
	if err != nil {
		return Proposal{}, newProviderError(MockProviderID, "correct", ErrorTransient, "encode", err)
	}
	proposal := Proposal{
		Changes: changes, RawJSON: raw,
		ProviderID: MockProviderID, ProfileID: provider.profile.ID,
		Model: provider.profile.Model, PromptVersion: PromptVersionV1,
	}
	if _, err := ValidateProposal(request, proposal); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

func isMockModel(value string) bool {
	return strings.TrimSpace(value) == "deterministic_glossary_v1"
}
