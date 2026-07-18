package llmprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/llm"
)

type fakeRepository struct {
	created CreateParams
	stored  StoredProfile
	enabled []StoredProfile
}

func (repository *fakeRepository) Create(_ context.Context, params CreateParams) (Profile, error) {
	repository.created = params
	config, err := decodeConfig(params.ConfigJSON)
	if err != nil {
		return Profile{}, err
	}
	return Profile{
		ID: params.ProfileID, WorkspaceID: params.WorkspaceID, ProviderID: params.ProviderID,
		DisplayName: params.DisplayName, Config: config, State: params.State, Priority: params.Priority,
		Version: 1, SecretConfigured: len(params.SecretCiphertext) > 0,
	}, nil
}
func (*fakeRepository) List(context.Context, string) ([]Profile, error) { return nil, nil }
func (repository *fakeRepository) ListEnabled(context.Context, string) ([]StoredProfile, error) {
	return repository.enabled, nil
}
func (repository *fakeRepository) GetStored(context.Context, string, string) (StoredProfile, error) {
	if repository.stored.ID == "" {
		return StoredProfile{}, ErrNotFound
	}
	return repository.stored, nil
}
func (*fakeRepository) Update(context.Context, UpdateParams) (Profile, error) {
	return Profile{}, nil
}
func (*fakeRepository) RecordHealth(context.Context, string, string, llm.ErrorClass) error {
	return nil
}

type fakeCipher struct {
	plaintext, aad []byte
	ciphertext     []byte
}

func (cipher *fakeCipher) Seal(plaintext, aad []byte) ([]byte, error) {
	cipher.plaintext = append([]byte(nil), plaintext...)
	cipher.aad = append([]byte(nil), aad...)
	return append([]byte(nil), cipher.ciphertext...), nil
}
func (cipher *fakeCipher) Open([]byte, []byte) ([]byte, error) {
	return append([]byte(nil), cipher.plaintext...), nil
}

func owner(scopes ...string) auth.Principal {
	return auth.Principal{
		UserID: "11111111-1111-4111-8111-111111111111", WorkspaceID: "22222222-2222-4222-8222-222222222222",
		Role: "owner", Scopes: scopes,
	}
}

func mockConfig() Config {
	profile := llm.DefaultMockProfile("ignored")
	return Config{
		Model: profile.Model, Timeout: profile.Timeout.String(), Concurrency: profile.Concurrency,
		Temperature: profile.Temperature, ContextLimit: profile.ContextLimit,
		StructuredOutput: true, PromptTemplate: profile.PromptTemplate,
		AutoApprovalPolicy: profile.AutoApprovalPolicy,
	}
}

func compatibleConfig() Config {
	return Config{
		BaseURL: "https://llm.example.com/v1", Model: "fixture-model",
		CustomHeaderNames: []string{"x-tenant"}, Timeout: "30s", Concurrency: 4,
		Temperature: 0, ContextLimit: 64000, StructuredOutput: true,
		PromptTemplate: llm.PromptVersionV1, AutoApprovalPolicy: "never",
	}
}

func TestCreateEncryptsAPIKeyAndHeaderValuesOnly(t *testing.T) {
	repository := &fakeRepository{}
	cipher := &fakeCipher{ciphertext: []byte("encrypted")}
	service := NewService(repository, cipher)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	credentials := json.RawMessage(`{"api_key":"fixture-api-key","custom_headers":{"X-Tenant":"secret-tenant"}}`)
	created, err := service.Create(context.Background(), owner(auth.ScopeAdminWrite), CreateInput{
		ProviderID: llm.OpenAICompatibleProviderID, DisplayName: " Compatible ",
		Config: compatibleConfig(), Credentials: credentials, State: StateEnabled, Priority: 10,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created.SecretConfigured || created.Config.CustomHeaderNames[0] != "X-Tenant" {
		t.Fatalf("created = %#v", created)
	}
	if !bytes.Equal(cipher.plaintext, credentials) || !bytes.Contains(cipher.aad, []byte(created.ID)) {
		t.Fatal("credentials were not sealed with record-bound associated data")
	}
	if bytes.Contains(repository.created.ConfigJSON, []byte("fixture-api-key")) ||
		bytes.Contains(repository.created.ConfigJSON, []byte("secret-tenant")) {
		t.Fatal("plaintext credential leaked into public config")
	}
}

func TestCreateRejectsHeaderMismatchAndUnavailableEncryption(t *testing.T) {
	input := CreateInput{
		ProviderID: llm.OpenAICompatibleProviderID, DisplayName: "Compatible", Config: compatibleConfig(),
		Credentials: json.RawMessage(`{"api_key":"fixture-api-key","custom_headers":{"X-Other":"value"}}`),
	}
	service := NewService(&fakeRepository{}, &fakeCipher{})
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	if _, err := service.Create(context.Background(), owner(auth.ScopeAdminWrite), input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("header mismatch error = %v", err)
	}
	input.Credentials = json.RawMessage(`{"api_key":"fixture-api-key","custom_headers":{"X-Tenant":"value"}}`)
	service = NewService(&fakeRepository{}, nil)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	if _, err := service.Create(context.Background(), owner(auth.ScopeAdminWrite), input); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("missing cipher error = %v", err)
	}
}

func TestCreateMockRequiresDeterministicModelAndNoCredentials(t *testing.T) {
	service := NewService(&fakeRepository{}, nil)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	input := CreateInput{ProviderID: llm.MockProviderID, DisplayName: "Mock", Config: mockConfig()}
	created, err := service.Create(context.Background(), owner(auth.ScopeAdminWrite), input)
	if err != nil || created.SecretConfigured {
		t.Fatalf("Create(mock) = %#v, %v", created, err)
	}
	input.Config.Model = "pretend-model"
	if _, err := service.Create(context.Background(), owner(auth.ScopeAdminWrite), input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid mock model error = %v", err)
	}
}

func TestResolverDefaultsToMockAndRejectsInvalidCiphertext(t *testing.T) {
	resolved, err := NewResolver(&fakeRepository{}, nil, nil).Resolve(context.Background(), "workspace")
	if err != nil || resolved.Provider.ID() != llm.MockProviderID {
		t.Fatalf("Resolve(default) = %#v, %v", resolved, err)
	}
	repository := &fakeRepository{enabled: []StoredProfile{{Profile: Profile{
		ID: "profile", WorkspaceID: "workspace", ProviderID: llm.OpenAICompatibleProviderID,
		Config: compatibleConfig(), SecretConfigured: true,
	}, SecretCiphertext: []byte("bad")}}}
	if _, err := NewResolver(repository, nil, nil).Resolve(context.Background(), "workspace"); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("Resolve(invalid secret) error = %v", err)
	}
}

func TestErrorsNeverIncludeCredentialValues(t *testing.T) {
	const secret = "credential-value-that-must-not-leak"
	service := NewService(&fakeRepository{}, &fakeCipher{})
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	_, err := service.Create(context.Background(), owner(auth.ScopeAdminWrite), CreateInput{
		ProviderID: llm.OpenAICompatibleProviderID, DisplayName: "Compatible", Config: compatibleConfig(),
		Credentials: json.RawMessage(`{"api_key":"` + secret + `","extra":"bad"}`),
	})
	if !errors.Is(err, ErrInvalidInput) || strings.Contains(err.Error(), secret) {
		t.Fatalf("error = %v", err)
	}
}
