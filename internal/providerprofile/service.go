package providerprofile

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

type SecretCipher interface {
	Seal(plaintext, associatedData []byte) ([]byte, error)
	Open(ciphertext, associatedData []byte) ([]byte, error)
}

type Repository interface {
	Create(ctx context.Context, params CreateParams) (Profile, error)
	List(ctx context.Context, workspaceID string) ([]Profile, error)
	ListEnabledASR(ctx context.Context, workspaceID string) ([]StoredProfile, error)
	GetStored(ctx context.Context, workspaceID, profileID string) (StoredProfile, error)
	Update(ctx context.Context, params UpdateParams) (Profile, error)
	RecordHealth(ctx context.Context, profileID, status string, errorClass asr.ErrorClass) error
}

type CreateParams struct {
	ProfileID        string
	AuditID          string
	WorkspaceID      string
	CreatedBy        string
	ProviderID       string
	DisplayName      string
	ConfigJSON       json.RawMessage
	SecretCiphertext []byte
	State            string
	Priority         int
}

type UpdateParams struct {
	ProfileID        string
	AuditID          string
	WorkspaceID      string
	UpdatedBy        string
	DisplayName      string
	ConfigJSON       json.RawMessage
	SecretCiphertext []byte
	State            string
	Priority         int
	ExpectedVersion  int64
}

type Service struct {
	repository Repository
	cipher     SecretCipher
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository, cipher SecretCipher) *Service {
	return &Service{repository: repository, cipher: cipher, random: rand.Reader, now: time.Now}
}

func (service *Service) Create(
	ctx context.Context,
	principal auth.Principal,
	input CreateInput,
) (Profile, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Profile{}, ErrForbidden
	}
	normalizeCreateInput(&input)
	if !validCreateInput(input) {
		return Profile{}, ErrInvalidInput
	}
	profileID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Profile{}, fmt.Errorf("generate provider profile ID: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Profile{}, fmt.Errorf("generate provider profile audit ID: %w", err)
	}
	asrProfile, err := input.Config.ASRProfile(profileID, input.ProviderID)
	if err != nil {
		return Profile{}, ErrInvalidInput
	}
	credentials := append(json.RawMessage(nil), bytes.TrimSpace(input.Credentials)...)
	defer clear(credentials)
	if input.ProviderID != asr.MockProviderID && len(credentials) == 0 {
		return Profile{}, ErrInvalidInput
	}
	if _, err := asr.NewConfiguredProvider(asrProfile, credentials, nil); err != nil {
		return Profile{}, ErrInvalidInput
	}
	if input.ProviderID == asr.MockProviderID {
		clear(credentials)
		credentials = nil
	}

	var ciphertext []byte
	if len(credentials) > 0 {
		if service.cipher == nil {
			return Profile{}, ErrEncryptionUnavailable
		}
		ciphertext, err = service.cipher.Seal(credentials, profileAssociatedData(
			principal.WorkspaceID, profileID, input.ProviderID,
		))
		if err != nil {
			return Profile{}, ErrConfiguration
		}
	}
	configJSON, err := json.Marshal(input.Config)
	if err != nil {
		return Profile{}, ErrInvalidInput
	}
	created, err := service.repository.Create(ctx, CreateParams{
		ProfileID: profileID, AuditID: auditID,
		WorkspaceID: principal.WorkspaceID, CreatedBy: principal.UserID,
		ProviderID: input.ProviderID, DisplayName: input.DisplayName,
		ConfigJSON: configJSON, SecretCiphertext: ciphertext,
		State: input.State, Priority: input.Priority,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return Profile{}, ErrConflict
		}
		return Profile{}, fmt.Errorf("create provider profile: %w", err)
	}
	return created, nil
}

func (service *Service) List(
	ctx context.Context,
	principal auth.Principal,
) ([]Profile, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return nil, ErrForbidden
	}
	profiles, err := service.repository.List(ctx, principal.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("list provider profiles: %w", err)
	}
	return profiles, nil
}

func (service *Service) Update(
	ctx context.Context,
	principal auth.Principal,
	profileID string,
	expectedVersion int64,
	input UpdateInput,
) (Profile, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Profile{}, ErrForbidden
	}
	if strings.TrimSpace(profileID) == "" || expectedVersion < 1 || !hasUpdate(input) {
		return Profile{}, ErrInvalidInput
	}
	stored, err := service.repository.GetStored(ctx, principal.WorkspaceID, profileID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Profile{}, ErrNotFound
		}
		return Profile{}, fmt.Errorf("load provider profile: %w", err)
	}
	if stored.Version != expectedVersion {
		return Profile{}, ErrConflict
	}
	displayName := stored.DisplayName
	if input.DisplayName != nil {
		displayName = strings.TrimSpace(*input.DisplayName)
	}
	config := stored.Config
	if input.Config != nil {
		config = *input.Config
	}
	state := stored.State
	if input.State != nil {
		state = strings.TrimSpace(*input.State)
	}
	priority := stored.Priority
	if input.Priority != nil {
		priority = *input.Priority
	}
	if !validDisplayName(displayName) || (state != StateEnabled && state != StateDisabled) ||
		priority < 1 || priority > 1000 {
		return Profile{}, ErrInvalidInput
	}
	asrProfile, err := config.ASRProfile(stored.ID, stored.ProviderID)
	if err != nil || asr.ValidateProfileDefinition(asrProfile) != nil {
		return Profile{}, ErrInvalidInput
	}
	ciphertext := append([]byte(nil), stored.SecretCiphertext...)
	credentials := append(json.RawMessage(nil), bytes.TrimSpace(input.Credentials)...)
	defer clear(credentials)
	if len(credentials) > 0 {
		if _, err := asr.NewConfiguredProvider(asrProfile, credentials, nil); err != nil {
			return Profile{}, ErrInvalidInput
		}
		if stored.ProviderID == asr.MockProviderID {
			ciphertext = nil
		} else {
			if service.cipher == nil {
				return Profile{}, ErrEncryptionUnavailable
			}
			ciphertext, err = service.cipher.Seal(credentials, profileAssociatedData(
				stored.WorkspaceID, stored.ID, stored.ProviderID,
			))
			if err != nil {
				return Profile{}, ErrConfiguration
			}
		}
	}
	secretConfigured := len(ciphertext) > 0
	if stored.ProviderID != asr.MockProviderID && !secretConfigured {
		return Profile{}, ErrInvalidInput
	}
	if state == StateEnabled && stored.ProviderID != asr.MockProviderID && service.cipher == nil {
		return Profile{}, ErrEncryptionUnavailable
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return Profile{}, ErrInvalidInput
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Profile{}, fmt.Errorf("generate provider profile audit ID: %w", err)
	}
	updated, err := service.repository.Update(ctx, UpdateParams{
		ProfileID: stored.ID, AuditID: auditID,
		WorkspaceID: stored.WorkspaceID, UpdatedBy: principal.UserID,
		DisplayName: displayName, ConfigJSON: configJSON, SecretCiphertext: ciphertext,
		State: state, Priority: priority, ExpectedVersion: expectedVersion,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return Profile{}, ErrConflict
		}
		if errors.Is(err, ErrNotFound) {
			return Profile{}, ErrNotFound
		}
		return Profile{}, fmt.Errorf("update provider profile: %w", err)
	}
	return updated, nil
}

func (service *Service) Health(
	ctx context.Context,
	principal auth.Principal,
	profileID string,
) (Health, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return Health{}, ErrForbidden
	}
	stored, err := service.repository.GetStored(ctx, principal.WorkspaceID, profileID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Health{}, ErrNotFound
		}
		return Health{}, fmt.Errorf("load provider profile health target: %w", err)
	}
	profile, err := stored.Config.ASRProfile(stored.ID, stored.ProviderID)
	if err != nil {
		return Health{}, ErrConfiguration
	}
	var credentials json.RawMessage
	if stored.SecretConfigured {
		if service.cipher == nil {
			return Health{}, ErrEncryptionUnavailable
		}
		credentials, err = service.cipher.Open(
			stored.SecretCiphertext,
			profileAssociatedData(stored.WorkspaceID, stored.ID, stored.ProviderID),
		)
		if err != nil {
			return Health{}, ErrConfiguration
		}
		defer clear(credentials)
	}
	provider, err := asr.NewConfiguredProvider(profile, credentials, nil)
	if err == nil {
		err = provider.Health(ctx)
	}
	result := Health{ProfileID: stored.ID, Status: "healthy", CheckedAt: service.now().UTC()}
	if err != nil {
		result.Status = "unhealthy"
		result.ErrorClass = asr.ErrorClassOf(err)
		if result.ErrorClass == "" {
			result.ErrorClass = asr.ErrorTransient
		}
	}
	if recordErr := service.repository.RecordHealth(
		ctx, stored.ID, result.Status, result.ErrorClass,
	); recordErr != nil {
		return Health{}, fmt.Errorf("record provider health: %w", recordErr)
	}
	return result, nil
}

func (service *Service) Capabilities(principal auth.Principal) ([]asr.Capabilities, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return nil, ErrForbidden
	}
	return asr.BuiltInCapabilities(), nil
}

func hasUpdate(input UpdateInput) bool {
	return input.DisplayName != nil || input.Config != nil || input.Credentials != nil ||
		input.State != nil || input.Priority != nil
}

func normalizeCreateInput(input *CreateInput) {
	input.ProviderID = strings.TrimSpace(input.ProviderID)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.State = strings.TrimSpace(input.State)
	if input.State == "" {
		input.State = StateDisabled
	}
	if input.Priority == 0 {
		input.Priority = 100
	}
}

func validCreateInput(input CreateInput) bool {
	if input.ProviderID != asr.MockProviderID && input.ProviderID != asr.AliyunProviderID &&
		input.ProviderID != asr.TencentProviderID {
		return false
	}
	if input.State != StateEnabled && input.State != StateDisabled {
		return false
	}
	if input.Priority < 1 || input.Priority > 1000 || !validDisplayName(input.DisplayName) {
		return false
	}
	return true
}

func validDisplayName(value string) bool {
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

func profileAssociatedData(workspaceID, profileID, providerID string) []byte {
	return []byte("voiceasset/provider-profile/v1\x00" + workspaceID + "\x00" + profileID + "\x00" + providerID)
}
