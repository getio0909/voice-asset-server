package webhook

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

const (
	webhookWorkspaceID = "10000000-0000-4000-8000-0000000000b1"
	webhookOwnerID     = "20000000-0000-4000-8000-0000000000b1"
	webhookEndpointID  = "40000000-0000-4000-8000-0000000000b1"
)

func TestServiceCreateGeneratesOneTimeEncryptedSecretAndNormalizesEndpoint(t *testing.T) {
	repository := &fakeRepository{}
	cipher := &fakeCipher{sealed: []byte("encrypted-only")}
	service := NewService(repository, cipher)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 128))
	service.now = func() time.Time { return time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC) }

	result, err := service.Create(context.Background(), ownerPrincipal(), CreateInput{
		DisplayName: "  Build receiver  ",
		URL:         " https://hooks.example.com:10443/voiceasset/events ",
		EventTypes:  []string{EventJobSucceeded, EventJobFailed, EventJobSucceeded},
		State:       StateEnabled,
	}, "request-create")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.HasPrefix(result.SigningSecret, "va_whsec_") || len(result.SigningSecret) < 50 {
		t.Fatalf("SigningSecret has unexpected shape")
	}
	if result.Endpoint.DisplayName != "Build receiver" ||
		result.Endpoint.URL != "https://hooks.example.com:10443/voiceasset/events" ||
		result.Endpoint.State != StateEnabled || !result.Endpoint.SecretConfigured {
		t.Fatalf("Endpoint = %+v", result.Endpoint)
	}
	wantEvents := []string{EventJobFailed, EventJobSucceeded}
	if !reflect.DeepEqual(result.Endpoint.EventTypes, wantEvents) ||
		!reflect.DeepEqual(repository.create.EventTypes, wantEvents) {
		t.Fatalf("EventTypes = %#v / %#v", result.Endpoint.EventTypes, repository.create.EventTypes)
	}
	if string(repository.create.SecretCiphertext) != "encrypted-only" ||
		bytes.Contains(repository.create.SecretCiphertext, []byte(result.SigningSecret)) {
		t.Fatal("repository received an unencrypted signing secret")
	}
	if string(cipher.plaintext) != result.SigningSecret ||
		!strings.Contains(string(cipher.associatedData), webhookWorkspaceID) ||
		!strings.Contains(string(cipher.associatedData), repository.create.EndpointID) ||
		!strings.HasSuffix(string(cipher.associatedData), "\x001") {
		t.Fatal("secret envelope was not bound to workspace, endpoint, and version")
	}
	if repository.create.RequestID != "request-create" || repository.create.CreatedBy != webhookOwnerID ||
		repository.create.Version != 1 {
		t.Fatalf("CreateParams = %+v", repository.create)
	}
}

func TestServiceCreateRejectsUnsafeUnauthorizedAndUnencryptedInputs(t *testing.T) {
	valid := CreateInput{
		DisplayName: "Receiver", URL: "https://hooks.example.com/events",
		EventTypes: []string{EventJobSucceeded}, State: StateEnabled,
	}
	tests := []struct {
		name      string
		principal auth.Principal
		input     CreateInput
		cipher    SecretCipher
		want      error
	}{
		{name: "api key", principal: apiKeyOwnerPrincipal(), input: valid, cipher: &fakeCipher{}, want: ErrForbidden},
		{name: "admin role", principal: adminPrincipal(), input: valid, cipher: &fakeCipher{}, want: ErrForbidden},
		{name: "private endpoint", principal: ownerPrincipal(), input: mutateCreate(valid, func(input *CreateInput) { input.URL = "https://127.0.0.1/events" }), cipher: &fakeCipher{}, want: ErrInvalidInput},
		{name: "query credential", principal: ownerPrincipal(), input: mutateCreate(valid, func(input *CreateInput) { input.URL += "?token=secret" }), cipher: &fakeCipher{}, want: ErrInvalidInput},
		{name: "unsupported event", principal: ownerPrincipal(), input: mutateCreate(valid, func(input *CreateInput) { input.EventTypes = []string{"asset.created"} }), cipher: &fakeCipher{}, want: ErrInvalidInput},
		{name: "no encryption", principal: ownerPrincipal(), input: valid, cipher: nil, want: ErrEncryptionUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(&fakeRepository{}, test.cipher)
			service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 128))
			if _, err := service.Create(context.Background(), test.principal, test.input, "request"); !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestServiceListRequiresOwnerSession(t *testing.T) {
	repository := &fakeRepository{items: []Endpoint{{
		ID: webhookEndpointID, WorkspaceID: webhookWorkspaceID,
		DisplayName: "Receiver", URL: "https://hooks.example.com/events",
		EventTypes: []string{EventJobSucceeded}, State: StateEnabled,
		Version: 1, SecretConfigured: true,
	}}}
	service := NewService(repository, &fakeCipher{})
	items, err := service.List(context.Background(), ownerPrincipal())
	if err != nil || len(items) != 1 || items[0].ID != webhookEndpointID {
		t.Fatalf("List() = (%+v, %v)", items, err)
	}
	if repository.listWorkspaceID != webhookWorkspaceID {
		t.Fatalf("List() workspace = %q", repository.listWorkspaceID)
	}
	for _, principal := range []auth.Principal{adminPrincipal(), apiKeyOwnerPrincipal()} {
		if _, err := service.List(context.Background(), principal); !errors.Is(err, ErrForbidden) {
			t.Fatalf("List(%+v) error = %v", principal, err)
		}
	}
}

func TestServiceUpdateUsesExactVersionAndCancelsRetargetingThroughRepository(t *testing.T) {
	stored := storedEndpointFixture()
	repository := &fakeRepository{stored: stored}
	service := NewService(repository, &fakeCipher{})
	newName := "Primary receiver"
	newURL := "https://new-hooks.example.com/events"
	newEvents := []string{EventJobCancelled, EventJobFailed, EventJobCancelled}
	newState := StateDisabled
	updated, err := service.Update(context.Background(), ownerPrincipal(), webhookEndpointID, 1, UpdateInput{
		DisplayName: &newName, URL: &newURL, EventTypes: &newEvents, State: &newState,
	}, "request-update")
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Version != 2 || updated.DisplayName != newName || updated.URL != newURL ||
		updated.State != StateDisabled ||
		!reflect.DeepEqual(updated.EventTypes, []string{EventJobCancelled, EventJobFailed}) {
		t.Fatalf("Update() = %+v", updated)
	}
	if repository.update.ExpectedVersion != 1 || repository.update.RequestID != "request-update" ||
		repository.update.UpdatedBy != webhookOwnerID {
		t.Fatalf("UpdateParams = %+v", repository.update)
	}

	if _, err := service.Update(context.Background(), ownerPrincipal(), webhookEndpointID, 2, UpdateInput{
		DisplayName: &newName,
	}, "request"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Update() error = %v", err)
	}
	unsafeURL := "https://10.0.0.1/events"
	if _, err := service.Update(context.Background(), ownerPrincipal(), webhookEndpointID, 1, UpdateInput{
		URL: &unsafeURL,
	}, "request"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unsafe Update() error = %v", err)
	}
}

func TestServiceRotateReturnsNewSecretOnceAndBindsNewSecretVersion(t *testing.T) {
	repository := &fakeRepository{stored: storedEndpointFixture()}
	cipher := &fakeCipher{sealed: []byte("rotated-ciphertext")}
	service := NewService(repository, cipher)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x37}, 128))
	result, err := service.RotateSecret(
		context.Background(), ownerPrincipal(), webhookEndpointID, 1, "request-rotate",
	)
	if err != nil {
		t.Fatalf("RotateSecret() error = %v", err)
	}
	if !strings.HasPrefix(result.SigningSecret, signingSecretPrefix) || result.Endpoint.Version != 2 {
		t.Fatalf("RotateSecret() = %+v", result)
	}
	if repository.rotate.ExpectedVersion != 1 || repository.rotate.SecretVersion != 2 ||
		string(repository.rotate.SecretCiphertext) != "rotated-ciphertext" ||
		repository.rotate.RequestID != "request-rotate" {
		t.Fatalf("RotateSecretParams = %+v", repository.rotate)
	}
	if string(cipher.plaintext) != result.SigningSecret ||
		!strings.HasSuffix(string(cipher.associatedData), "\x002") {
		t.Fatal("rotated secret is not bound to secret version 2")
	}
}

func TestServiceEnqueueTestCreatesOnlySafeVersionBoundPayload(t *testing.T) {
	now := time.Date(2026, 7, 18, 10, 30, 0, 0, time.UTC)
	repository := &fakeRepository{stored: storedEndpointFixture()}
	service := NewService(repository, &fakeCipher{})
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x51}, 128))
	service.now = func() time.Time { return now }
	delivery, err := service.EnqueueTest(
		context.Background(), ownerPrincipal(), webhookEndpointID, "request-test",
	)
	if err != nil {
		t.Fatalf("EnqueueTest() error = %v", err)
	}
	if delivery.EventType != EventWebhookTest || delivery.State != DeliveryPending ||
		delivery.WebhookVersion != 1 || delivery.Attempts != 0 {
		t.Fatalf("EnqueueTest() = %+v", delivery)
	}
	payload := string(repository.enqueueTest.Payload)
	for _, forbidden := range []string{webhookWorkspaceID, webhookOwnerID, "ciphertext", "signing_secret"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("test payload contains forbidden value category")
		}
	}
	for _, required := range []string{`"type":"webhook.test"`, `"webhook":{"id":"` + webhookEndpointID + `"}`} {
		if !strings.Contains(payload, required) {
			t.Fatalf("test payload = %s", payload)
		}
	}
	if repository.enqueueTest.RequestID != "request-test" ||
		repository.enqueueTest.WebhookVersion != 1 {
		t.Fatalf("EnqueueTestParams = %+v", repository.enqueueTest)
	}

	repository.stored.State = StateDisabled
	if _, err := service.EnqueueTest(context.Background(), ownerPrincipal(), webhookEndpointID, "request"); !errors.Is(err, ErrConflict) {
		t.Fatalf("disabled EnqueueTest() error = %v", err)
	}
}

func mutateCreate(input CreateInput, mutate func(*CreateInput)) CreateInput {
	mutate(&input)
	return input
}

func ownerPrincipal() auth.Principal {
	return auth.Principal{
		UserID: webhookOwnerID, WorkspaceID: webhookWorkspaceID, Role: "owner",
		Scopes: []string{auth.ScopeAdminRead, auth.ScopeAdminWrite}, CredentialType: "session",
	}
}

func adminPrincipal() auth.Principal {
	principal := ownerPrincipal()
	principal.Role = "admin"
	return principal
}

func apiKeyOwnerPrincipal() auth.Principal {
	principal := ownerPrincipal()
	principal.CredentialType = "api_key"
	return principal
}

type fakeCipher struct {
	sealed         []byte
	plaintext      []byte
	associatedData []byte
}

func (cipher *fakeCipher) Seal(plaintext, associatedData []byte) ([]byte, error) {
	cipher.plaintext = append([]byte(nil), plaintext...)
	cipher.associatedData = append([]byte(nil), associatedData...)
	if cipher.sealed == nil {
		return []byte("ciphertext"), nil
	}
	return append([]byte(nil), cipher.sealed...), nil
}

func (*fakeCipher) Open([]byte, []byte) ([]byte, error) { return nil, errors.New("not implemented") }

type fakeRepository struct {
	create          CreateParams
	update          UpdateParams
	rotate          RotateSecretParams
	enqueueTest     EnqueueTestParams
	stored          StoredEndpoint
	items           []Endpoint
	listWorkspaceID string
}

func (repository *fakeRepository) Create(_ context.Context, params CreateParams) (Endpoint, error) {
	repository.create = params
	return Endpoint{
		ID: params.EndpointID, WorkspaceID: params.WorkspaceID,
		DisplayName: params.DisplayName, URL: params.URL,
		EventTypes: append([]string(nil), params.EventTypes...), State: params.State,
		Version: params.Version, SecretConfigured: len(params.SecretCiphertext) > 0,
		CreatedAt: time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC),
	}, nil
}

func (repository *fakeRepository) List(_ context.Context, workspaceID string) ([]Endpoint, error) {
	repository.listWorkspaceID = workspaceID
	return append([]Endpoint(nil), repository.items...), nil
}

func (repository *fakeRepository) ListDeliveries(_ context.Context, _, _ string, _ int) ([]Delivery, error) {
	return nil, nil
}

func (repository *fakeRepository) GetStored(_ context.Context, workspaceID, endpointID string) (StoredEndpoint, error) {
	if repository.stored.ID == "" || repository.stored.WorkspaceID != workspaceID || repository.stored.ID != endpointID {
		return StoredEndpoint{}, ErrNotFound
	}
	return repository.stored, nil
}

func (repository *fakeRepository) Update(_ context.Context, params UpdateParams) (Endpoint, error) {
	repository.update = params
	return Endpoint{
		ID: params.EndpointID, WorkspaceID: params.WorkspaceID,
		DisplayName: params.DisplayName, URL: params.URL,
		EventTypes: append([]string(nil), params.EventTypes...), State: params.State,
		Version: params.ExpectedVersion + 1, SecretConfigured: true,
	}, nil
}

func (repository *fakeRepository) RotateSecret(_ context.Context, params RotateSecretParams) (Endpoint, error) {
	repository.rotate = params
	result := repository.stored.Endpoint
	result.Version = params.ExpectedVersion + 1
	result.SecretConfigured = true
	return result, nil
}

func (repository *fakeRepository) EnqueueTest(_ context.Context, params EnqueueTestParams) (Delivery, error) {
	repository.enqueueTest = params
	return Delivery{
		ID: params.DeliveryID, WebhookID: params.WebhookID,
		WebhookVersion: params.WebhookVersion, EventID: params.EventID,
		EventType: EventWebhookTest, State: DeliveryPending,
		MaxAttempts: 5, CreatedAt: params.CreatedAt, UpdatedAt: params.CreatedAt,
	}, nil
}

func storedEndpointFixture() StoredEndpoint {
	return StoredEndpoint{
		Endpoint: Endpoint{
			ID: webhookEndpointID, WorkspaceID: webhookWorkspaceID,
			DisplayName: "Receiver", URL: "https://hooks.example.com/events",
			EventTypes: []string{EventJobSucceeded}, State: StateEnabled,
			Version: 1, SecretConfigured: true,
		},
		SecretVersion: 1, SecretCiphertext: []byte("ciphertext"),
	}
}

var _ Repository = (*fakeRepository)(nil)
