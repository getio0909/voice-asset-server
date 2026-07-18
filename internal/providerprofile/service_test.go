package providerprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/auth"
)

type fakeRepository struct {
	createParams CreateParams
	createResult Profile
	createErr    error
	listResult   []Profile
	listErr      error
	enabled      []StoredProfile
	enabledErr   error
	workspaceID  string
	stored       StoredProfile
	getErr       error
	updateParams UpdateParams
	updateResult Profile
	updateErr    error
	healthStatus string
	healthClass  asr.ErrorClass
	healthErr    error
}

func (repository *fakeRepository) Create(_ context.Context, params CreateParams) (Profile, error) {
	repository.createParams = params
	if repository.createErr != nil {
		return Profile{}, repository.createErr
	}
	result := repository.createResult
	result.ID = params.ProfileID
	result.WorkspaceID = params.WorkspaceID
	result.ProviderID = params.ProviderID
	result.DisplayName = params.DisplayName
	result.State = params.State
	result.Priority = params.Priority
	result.SecretConfigured = len(params.SecretCiphertext) > 0
	config, err := decodeConfig(params.ConfigJSON)
	if err != nil {
		return Profile{}, err
	}
	result.Config = config
	return result, nil
}

func (repository *fakeRepository) List(_ context.Context, workspaceID string) ([]Profile, error) {
	repository.workspaceID = workspaceID
	return append([]Profile(nil), repository.listResult...), repository.listErr
}

func (repository *fakeRepository) ListEnabledASR(_ context.Context, workspaceID string) ([]StoredProfile, error) {
	repository.workspaceID = workspaceID
	return append([]StoredProfile(nil), repository.enabled...), repository.enabledErr
}

func (repository *fakeRepository) GetStored(_ context.Context, workspaceID, _ string) (StoredProfile, error) {
	repository.workspaceID = workspaceID
	return repository.stored, repository.getErr
}

func (repository *fakeRepository) Update(_ context.Context, params UpdateParams) (Profile, error) {
	repository.updateParams = params
	return repository.updateResult, repository.updateErr
}

func (repository *fakeRepository) RecordHealth(
	_ context.Context,
	_ string,
	status string,
	errorClass asr.ErrorClass,
) error {
	repository.healthStatus = status
	repository.healthClass = errorClass
	return repository.healthErr
}

type fakeCipher struct {
	sealed     []byte
	sealAAD    []byte
	opened     []byte
	openAAD    []byte
	sealErr    error
	openErr    error
	ciphertext []byte
}

func (cipher *fakeCipher) Seal(plaintext, associatedData []byte) ([]byte, error) {
	cipher.sealed = append([]byte(nil), plaintext...)
	cipher.sealAAD = append([]byte(nil), associatedData...)
	return append([]byte(nil), cipher.ciphertext...), cipher.sealErr
}

func (cipher *fakeCipher) Open(_ []byte, associatedData []byte) ([]byte, error) {
	cipher.openAAD = append([]byte(nil), associatedData...)
	return append([]byte(nil), cipher.opened...), cipher.openErr
}

func adminPrincipal(scopes ...string) auth.Principal {
	return auth.Principal{
		UserID:      "20000000-0000-4000-8000-000000000001",
		WorkspaceID: "10000000-0000-4000-8000-000000000001",
		Scopes:      scopes,
	}
}

func tencentConfig() ASRConfig {
	profile := asr.DefaultTencentFlashProfile("1234567890")
	return ASRConfig{
		Model: profile.Model, Language: profile.Language,
		SampleRate: profile.SampleRate, AudioFormat: profile.AudioFormat,
		Punctuation: profile.Punctuation, Timestamps: profile.Timestamps,
		WordTimestamps: profile.WordTimestamps, NumberNormalization: profile.NumberNormalization,
		Timeout: profile.Timeout.String(),
		Retry: RetryConfig{
			MaxAttempts: profile.Retry.MaxAttempts,
			BaseDelay:   profile.Retry.BaseDelay.String(), MaxDelay: profile.Retry.MaxDelay.String(),
		},
		Concurrency: profile.Concurrency, VendorExtension: append(json.RawMessage(nil), profile.VendorExtension...),
	}
}

func mockConfig() ASRConfig {
	profile := asr.DefaultMockProfile("ignored")
	return ASRConfig{
		Model: profile.Model, Language: profile.Language,
		SampleRate: profile.SampleRate, AudioFormat: profile.AudioFormat,
		Punctuation: profile.Punctuation, Timestamps: profile.Timestamps,
		WordTimestamps: profile.WordTimestamps,
		Timeout:        profile.Timeout.String(),
		Retry: RetryConfig{
			MaxAttempts: profile.Retry.MaxAttempts,
			BaseDelay:   profile.Retry.BaseDelay.String(), MaxDelay: profile.Retry.MaxDelay.String(),
		},
		Concurrency: profile.Concurrency, VendorExtension: json.RawMessage(`{}`),
	}
}

func TestServiceCreatesEncryptedWorkspaceScopedProfile(t *testing.T) {
	repository := &fakeRepository{createResult: Profile{Version: 1}}
	cipher := &fakeCipher{ciphertext: []byte("encrypted-fixture")}
	service := NewService(repository, cipher)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	credentials := json.RawMessage(`{"secret_id":"fixture-secret-id","secret_key":"fixture-secret-key"}`)
	input := CreateInput{
		ProviderID: asr.TencentProviderID, DisplayName: " Tencent primary ",
		Config: tencentConfig(), Credentials: credentials, State: StateEnabled, Priority: 10,
	}
	principal := adminPrincipal(auth.ScopeAdminWrite)
	created, err := service.Create(context.Background(), principal, input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.WorkspaceID != principal.WorkspaceID || created.DisplayName != "Tencent primary" ||
		created.ProviderID != asr.TencentProviderID || !created.SecretConfigured {
		t.Fatalf("Create() = %+v", created)
	}
	if !bytes.Equal(cipher.sealed, credentials) ||
		!bytes.Equal(cipher.sealAAD, profileAssociatedData(principal.WorkspaceID, created.ID, asr.TencentProviderID)) {
		t.Fatal("credentials were not encrypted with record-bound associated data")
	}
	if !bytes.Equal(repository.createParams.SecretCiphertext, cipher.ciphertext) ||
		bytes.Contains(repository.createParams.ConfigJSON, []byte("fixture-secret")) {
		t.Fatal("repository received plaintext credentials outside the ciphertext field")
	}
	if !bytes.Equal(input.Credentials, credentials) {
		t.Fatal("Create() mutated caller-owned credential JSON")
	}
}

func TestServiceRejectsCredentialsWhenEncryptionIsUnavailableOrInvalid(t *testing.T) {
	input := CreateInput{
		ProviderID: asr.TencentProviderID, DisplayName: "Tencent",
		Config:      tencentConfig(),
		Credentials: json.RawMessage(`{"secret_id":"fixture-secret-id","secret_key":"fixture-secret-key"}`),
	}
	service := NewService(&fakeRepository{}, nil)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	if _, err := service.Create(context.Background(), adminPrincipal(auth.ScopeAdminWrite), input); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("Create() error = %v, want ErrEncryptionUnavailable", err)
	}

	cipher := &fakeCipher{ciphertext: []byte("encrypted")}
	service = NewService(&fakeRepository{}, cipher)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	input.Credentials = json.RawMessage(`{"secret_id":"fixture-secret-id","secret_key":"fixture-secret-key","extra":"bad"}`)
	if _, err := service.Create(context.Background(), adminPrincipal(auth.ScopeAdminWrite), input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create(invalid secret) error = %v", err)
	}
	if len(cipher.sealed) != 0 {
		t.Fatal("invalid credentials reached the encryption boundary")
	}
}

func TestServiceAllowsCredentialFreeMockAndEnforcesAdminScopes(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, nil)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	input := CreateInput{ProviderID: asr.MockProviderID, DisplayName: "Mock", Config: mockConfig()}
	if _, err := service.Create(context.Background(), adminPrincipal(auth.ScopeAdminRead), input); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Create(non-admin-write) error = %v", err)
	}
	created, err := service.Create(context.Background(), adminPrincipal(auth.ScopeAdminWrite), input)
	if err != nil {
		t.Fatalf("Create(mock) error = %v", err)
	}
	if created.State != StateDisabled || created.Priority != 100 || created.SecretConfigured {
		t.Fatalf("defaulted mock profile = %+v", created)
	}

	repository.listResult = []Profile{created}
	if _, err := service.List(context.Background(), adminPrincipal(auth.ScopeAssetsRead)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("List(non-admin) error = %v", err)
	}
	listed, err := service.List(context.Background(), adminPrincipal(auth.ScopeAdminRead))
	if err != nil || len(listed) != 1 || repository.workspaceID != adminPrincipal().WorkspaceID {
		t.Fatalf("List() = %+v, error = %v, workspace = %q", listed, err, repository.workspaceID)
	}
}

func TestConfigDecoderRejectsUnknownAndTrailingJSON(t *testing.T) {
	configJSON, err := json.Marshal(tencentConfig())
	if err != nil {
		t.Fatal(err)
	}
	withUnknown := append([]byte(`{"unknown":true,`), configJSON[1:]...)
	if _, err := decodeConfig(withUnknown); err == nil {
		t.Fatal("decodeConfig() accepted an unknown field")
	}
	if _, err := decodeConfig(append(configJSON, []byte(` {}`)...)); err == nil {
		t.Fatal("decodeConfig() accepted trailing JSON")
	}
}

func TestProfileErrorsDoNotContainCredentialValues(t *testing.T) {
	const secret = "credential-value-that-must-not-leak"
	input := CreateInput{
		ProviderID: asr.TencentProviderID, DisplayName: "Tencent", Config: tencentConfig(),
		Credentials: json.RawMessage(`{"secret_id":"short","secret_key":"` + secret + `"}`),
	}
	service := NewService(&fakeRepository{}, &fakeCipher{})
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	_, err := service.Create(context.Background(), adminPrincipal(auth.ScopeAdminWrite), input)
	if !errors.Is(err, ErrInvalidInput) || strings.Contains(err.Error(), secret) {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestServiceUpdatesProfileOptimisticallyAndPreservesCiphertext(t *testing.T) {
	config := tencentConfig()
	ciphertext := bytes.Repeat([]byte{0x5a}, 64)
	stored := StoredProfile{
		Profile: Profile{
			ID:          "90000000-0000-4000-8000-000000000001",
			WorkspaceID: adminPrincipal().WorkspaceID,
			ProviderID:  asr.TencentProviderID, DisplayName: "Tencent",
			Config: config, State: StateEnabled, Priority: 10, Version: 2,
			SecretConfigured: true,
		},
		SecretCiphertext: ciphertext,
	}
	repository := &fakeRepository{stored: stored, updateResult: Profile{
		ID: stored.ID, WorkspaceID: stored.WorkspaceID, ProviderID: stored.ProviderID,
		DisplayName: stored.DisplayName, Config: config, State: StateEnabled,
		Priority: 20, Version: 3, SecretConfigured: true,
	}}
	service := NewService(repository, &fakeCipher{})
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 32))
	priority := 20
	updated, err := service.Update(
		context.Background(), adminPrincipal(auth.ScopeAdminWrite), stored.ID, 2,
		UpdateInput{Priority: &priority},
	)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Version != 3 || repository.updateParams.ExpectedVersion != 2 ||
		repository.updateParams.Priority != 20 ||
		!bytes.Equal(repository.updateParams.SecretCiphertext, ciphertext) {
		t.Fatalf("Update() = %+v, params = %+v", updated, repository.updateParams)
	}
	if !bytes.Equal(stored.SecretCiphertext, ciphertext) {
		t.Fatal("Update() mutated repository-owned ciphertext")
	}
}

func TestServiceUpdateReplacesCredentialsAndRejectsStaleVersion(t *testing.T) {
	stored := StoredProfile{Profile: Profile{
		ID:          "90000000-0000-4000-8000-000000000001",
		WorkspaceID: adminPrincipal().WorkspaceID,
		ProviderID:  asr.TencentProviderID, DisplayName: "Tencent",
		Config: tencentConfig(), State: StateDisabled, Priority: 10, Version: 2,
		SecretConfigured: true,
	}, SecretCiphertext: bytes.Repeat([]byte{0x5a}, 64)}
	repository := &fakeRepository{stored: stored, updateResult: Profile{Version: 3}}
	cipher := &fakeCipher{ciphertext: []byte("replacement-ciphertext")}
	service := NewService(repository, cipher)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 32))
	credentials := json.RawMessage(`{"secret_id":"replacement-id","secret_key":"replacement-key"}`)
	if _, err := service.Update(
		context.Background(), adminPrincipal(auth.ScopeAdminWrite), stored.ID, 2,
		UpdateInput{Credentials: credentials},
	); err != nil {
		t.Fatalf("Update(credentials) error = %v", err)
	}
	if !bytes.Equal(cipher.sealed, credentials) ||
		!bytes.Equal(repository.updateParams.SecretCiphertext, cipher.ciphertext) {
		t.Fatal("replacement credentials were not validated and encrypted")
	}

	repository.stored.Version = 3
	if _, err := service.Update(
		context.Background(), adminPrincipal(auth.ScopeAdminWrite), stored.ID, 2,
		UpdateInput{Credentials: credentials},
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(stale) error = %v", err)
	}
}

func TestServiceReportsAndRecordsMockHealthAndCapabilities(t *testing.T) {
	now := time.Date(2026, 7, 16, 13, 0, 0, 0, time.UTC)
	repository := &fakeRepository{stored: StoredProfile{Profile: Profile{
		ID:          "90000000-0000-4000-8000-000000000001",
		WorkspaceID: adminPrincipal().WorkspaceID,
		ProviderID:  asr.MockProviderID, DisplayName: "Mock", Config: mockConfig(),
		State: StateEnabled, Priority: 100, Version: 1,
	}}}
	service := NewService(repository, nil)
	service.now = func() time.Time { return now }
	health, err := service.Health(
		context.Background(), adminPrincipal(auth.ScopeAdminRead), repository.stored.ID,
	)
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.Status != "healthy" || health.ErrorClass != "" || !health.CheckedAt.Equal(now) ||
		repository.healthStatus != "healthy" || repository.healthClass != "" {
		t.Fatalf("Health() = %+v, recorded = %q/%q", health, repository.healthStatus, repository.healthClass)
	}
	capabilities, err := service.Capabilities(adminPrincipal(auth.ScopeAdminRead))
	if err != nil || len(capabilities) != 3 || capabilities[0].ProviderID != asr.MockProviderID {
		t.Fatalf("Capabilities() = %+v, error = %v", capabilities, err)
	}
	if _, err := service.Capabilities(adminPrincipal(auth.ScopeAssetsRead)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Capabilities(non-admin) error = %v", err)
	}
}
