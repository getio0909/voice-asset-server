package llmprofile

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

type SecretCipher interface {
	Seal([]byte, []byte) ([]byte, error)
	Open([]byte, []byte) ([]byte, error)
}

type Repository interface {
	Create(context.Context, CreateParams) (Profile, error)
	List(context.Context, string) ([]Profile, error)
	ListEnabled(context.Context, string) ([]StoredProfile, error)
	GetStored(context.Context, string, string) (StoredProfile, error)
	Update(context.Context, UpdateParams) (Profile, error)
	RecordHealth(context.Context, string, string, llm.ErrorClass) error
}

type CreateParams struct {
	ProfileID, AuditID, WorkspaceID, CreatedBy string
	ProviderID, DisplayName, State             string
	ConfigJSON                                 json.RawMessage
	SecretCiphertext                           []byte
	Priority                                   int
}

type UpdateParams struct {
	ProfileID, AuditID, WorkspaceID, UpdatedBy string
	DisplayName, State                         string
	ConfigJSON                                 json.RawMessage
	SecretCiphertext                           []byte
	Priority                                   int
	ExpectedVersion                            int64
}

type Service struct {
	repository Repository
	cipher     SecretCipher
	client     *http.Client
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository, cipher SecretCipher) *Service {
	return &Service{repository: repository, cipher: cipher, random: rand.Reader, now: time.Now}
}

func (service *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput) (Profile, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Profile{}, ErrForbidden
	}
	normalizeCreate(&input)
	if !validBasic(input.ProviderID, input.DisplayName, input.State, input.Priority) {
		return Profile{}, ErrInvalidInput
	}
	profileID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Profile{}, fmt.Errorf("generate LLM profile ID: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Profile{}, fmt.Errorf("generate LLM profile audit ID: %w", err)
	}
	profile, err := input.Config.ProviderProfile(profileID, input.ProviderID)
	if err != nil || llm.ValidateProfileDefinition(profile) != nil || !validGlossaryID(input.Config.DefaultGlossaryID) {
		return Profile{}, ErrInvalidInput
	}
	credentials := append(json.RawMessage(nil), bytes.TrimSpace(input.Credentials)...)
	defer clear(credentials)
	if input.ProviderID != llm.MockProviderID && len(credentials) == 0 {
		return Profile{}, ErrInvalidInput
	}
	if _, err := llm.NewConfiguredProvider(profile, credentials, service.client); err != nil {
		return Profile{}, ErrInvalidInput
	}
	if !credentialHeadersMatch(input.Config.CustomHeaderNames, credentials, input.ProviderID) {
		return Profile{}, ErrInvalidInput
	}
	if input.ProviderID == llm.MockProviderID {
		credentials = nil
	}
	var ciphertext []byte
	if len(credentials) > 0 {
		if service.cipher == nil {
			return Profile{}, ErrEncryptionUnavailable
		}
		ciphertext, err = service.cipher.Seal(credentials, associatedData(principal.WorkspaceID, profileID, input.ProviderID))
		if err != nil {
			return Profile{}, ErrConfiguration
		}
	}
	configJSON, err := json.Marshal(input.Config)
	if err != nil {
		return Profile{}, ErrInvalidInput
	}
	created, err := service.repository.Create(ctx, CreateParams{
		ProfileID: profileID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		CreatedBy: principal.UserID, ProviderID: input.ProviderID, DisplayName: input.DisplayName,
		ConfigJSON: configJSON, SecretCiphertext: ciphertext, State: input.State, Priority: input.Priority,
	})
	return created, publicRepositoryError("create LLM profile", err)
}

func (service *Service) List(ctx context.Context, principal auth.Principal) ([]Profile, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return nil, ErrForbidden
	}
	profiles, err := service.repository.List(ctx, principal.WorkspaceID)
	if err != nil {
		return nil, publicRepositoryError("list LLM profiles", err)
	}
	return profiles, nil
}

func (service *Service) Update(ctx context.Context, principal auth.Principal, profileID string, expected int64, input UpdateInput) (Profile, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Profile{}, ErrForbidden
	}
	profileID, ok := identifier.NormalizeUUID(profileID)
	if !ok || expected < 1 || !hasUpdate(input) {
		return Profile{}, ErrInvalidInput
	}
	stored, err := service.repository.GetStored(ctx, principal.WorkspaceID, profileID)
	if err != nil {
		return Profile{}, publicRepositoryError("load LLM profile", err)
	}
	if stored.Version != expected {
		return Profile{}, ErrConflict
	}
	displayName, config, state, priority := stored.DisplayName, stored.Config, stored.State, stored.Priority
	if input.DisplayName != nil {
		displayName = strings.TrimSpace(*input.DisplayName)
	}
	if input.Config != nil {
		config = *input.Config
	}
	if input.State != nil {
		state = strings.TrimSpace(*input.State)
	}
	if input.Priority != nil {
		priority = *input.Priority
	}
	if !validBasic(stored.ProviderID, displayName, state, priority) {
		return Profile{}, ErrInvalidInput
	}
	profile, err := config.ProviderProfile(stored.ID, stored.ProviderID)
	if err != nil || llm.ValidateProfileDefinition(profile) != nil || !validGlossaryID(config.DefaultGlossaryID) {
		return Profile{}, ErrInvalidInput
	}
	ciphertext := append([]byte(nil), stored.SecretCiphertext...)
	credentials := append(json.RawMessage(nil), bytes.TrimSpace(input.Credentials)...)
	defer clear(credentials)
	if len(credentials) > 0 {
		if _, err := llm.NewConfiguredProvider(profile, credentials, service.client); err != nil ||
			!credentialHeadersMatch(config.CustomHeaderNames, credentials, stored.ProviderID) {
			return Profile{}, ErrInvalidInput
		}
		if stored.ProviderID == llm.MockProviderID {
			ciphertext = nil
		} else {
			if service.cipher == nil {
				return Profile{}, ErrEncryptionUnavailable
			}
			ciphertext, err = service.cipher.Seal(credentials, associatedData(stored.WorkspaceID, stored.ID, stored.ProviderID))
			if err != nil {
				return Profile{}, ErrConfiguration
			}
		}
	} else if !slices.Equal(normalizeHeaderNames(config.CustomHeaderNames), normalizeHeaderNames(stored.Config.CustomHeaderNames)) {
		// Header values are encrypted, so a header-set change must arrive with
		// a matching replacement credential document.
		return Profile{}, ErrInvalidInput
	}
	if stored.ProviderID != llm.MockProviderID && len(ciphertext) == 0 {
		return Profile{}, ErrInvalidInput
	}
	config.CustomHeaderNames = normalizeHeaderNames(config.CustomHeaderNames)
	configJSON, err := json.Marshal(config)
	if err != nil {
		return Profile{}, ErrInvalidInput
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Profile{}, fmt.Errorf("generate LLM profile audit ID: %w", err)
	}
	updated, err := service.repository.Update(ctx, UpdateParams{
		ProfileID: stored.ID, AuditID: auditID, WorkspaceID: stored.WorkspaceID,
		UpdatedBy: principal.UserID, DisplayName: displayName, ConfigJSON: configJSON,
		SecretCiphertext: ciphertext, State: state, Priority: priority, ExpectedVersion: expected,
	})
	return updated, publicRepositoryError("update LLM profile", err)
}

func (service *Service) Health(ctx context.Context, principal auth.Principal, profileID string) (Health, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return Health{}, ErrForbidden
	}
	profileID, ok := identifier.NormalizeUUID(profileID)
	if !ok {
		return Health{}, ErrNotFound
	}
	stored, err := service.repository.GetStored(ctx, principal.WorkspaceID, profileID)
	if err != nil {
		return Health{}, publicRepositoryError("load LLM health target", err)
	}
	profile, err := stored.Config.ProviderProfile(stored.ID, stored.ProviderID)
	if err != nil {
		return Health{}, ErrConfiguration
	}
	secret, err := service.openSecret(stored)
	if err != nil {
		return Health{}, err
	}
	defer clear(secret)
	provider, err := llm.NewConfiguredProvider(profile, secret, service.client)
	if err == nil {
		err = provider.Health(ctx)
	}
	result := Health{ProfileID: stored.ID, Status: "healthy", CheckedAt: service.now().UTC()}
	if err != nil {
		result.Status = "unhealthy"
		result.ErrorClass = llm.ErrorClassOf(err)
		if result.ErrorClass == "" {
			result.ErrorClass = llm.ErrorTransient
		}
	}
	if recordErr := service.repository.RecordHealth(ctx, stored.ID, result.Status, result.ErrorClass); recordErr != nil {
		return Health{}, publicRepositoryError("record LLM health", recordErr)
	}
	return result, nil
}

func (service *Service) Capabilities(principal auth.Principal) ([]llm.Capabilities, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return nil, ErrForbidden
	}
	return llm.BuiltInCapabilities(), nil
}

func (service *Service) openSecret(stored StoredProfile) (json.RawMessage, error) {
	if !stored.SecretConfigured {
		return nil, nil
	}
	if service.cipher == nil || len(stored.SecretCiphertext) == 0 {
		return nil, ErrEncryptionUnavailable
	}
	secret, err := service.cipher.Open(stored.SecretCiphertext, associatedData(stored.WorkspaceID, stored.ID, stored.ProviderID))
	if err != nil {
		return nil, ErrConfiguration
	}
	return secret, nil
}

func normalizeCreate(input *CreateInput) {
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.State = strings.TrimSpace(input.State)
	if input.State == "" {
		input.State = StateDisabled
	}
	if input.Priority == 0 {
		input.Priority = 100
	}
	input.Config.BaseURL = strings.TrimSpace(input.Config.BaseURL)
	input.Config.Model = strings.TrimSpace(input.Config.Model)
	input.Config.PromptTemplate = strings.TrimSpace(input.Config.PromptTemplate)
	input.Config.DefaultGlossaryID = strings.TrimSpace(input.Config.DefaultGlossaryID)
	input.Config.AutoApprovalPolicy = strings.TrimSpace(input.Config.AutoApprovalPolicy)
	input.Config.CustomHeaderNames = normalizeHeaderNames(input.Config.CustomHeaderNames)
}

func validBasic(providerID, name, state string, priority int) bool {
	return (providerID == llm.MockProviderID || providerID == llm.OpenAICompatibleProviderID) &&
		validName(name) && (state == StateEnabled || state == StateDisabled) && priority >= 1 && priority <= 1000
}

func validName(value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 || utf8.RuneCountInString(value) > 100 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func credentialHeadersMatch(expected []string, raw json.RawMessage, providerID string) bool {
	if providerID == llm.MockProviderID {
		return len(expected) == 0
	}
	var credentials struct {
		APIKey        string            `json:"api_key"`
		CustomHeaders map[string]string `json:"custom_headers,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil {
		return false
	}
	names := make([]string, 0, len(credentials.CustomHeaders))
	for name := range credentials.CustomHeaders {
		names = append(names, http.CanonicalHeaderKey(strings.TrimSpace(name)))
	}
	return slices.Equal(normalizeHeaderNames(expected), normalizeHeaderNames(names))
}

func normalizeHeaderNames(names []string) []string {
	result := make([]string, 0, len(names))
	seen := make(map[string]struct{})
	for _, name := range names {
		name = http.CanonicalHeaderKey(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func hasUpdate(input UpdateInput) bool {
	return input.DisplayName != nil || input.Config != nil || input.Credentials != nil || input.State != nil || input.Priority != nil
}

func validGlossaryID(value string) bool {
	if strings.TrimSpace(value) == "" {
		return true
	}
	_, ok := identifier.NormalizeUUID(value)
	return ok
}

func publicRepositoryError(operation string, err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{ErrConflict, ErrNotFound, ErrInvalidInput} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return fmt.Errorf("%w: %s", ErrConfiguration, operation)
}

func associatedData(workspaceID, profileID, providerID string) []byte {
	return []byte("voiceasset/llm-profile/v1\x00" + workspaceID + "\x00" + profileID + "\x00" + providerID)
}
