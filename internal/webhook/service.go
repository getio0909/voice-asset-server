// Package webhook manages workspace-scoped outbound Webhook endpoints and
// their durable delivery lifecycle.
package webhook

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
	"github.com/getio0909/voice-asset-server/internal/platform/outboundhttp"
)

const (
	StateEnabled  = "enabled"
	StateDisabled = "disabled"

	EventJobSucceeded = "job.succeeded"
	EventJobFailed    = "job.failed"
	EventJobCancelled = "job.cancelled"
	EventWebhookTest  = "webhook.test"

	DeliveryPending    = "pending"
	DeliveryDelivering = "delivering"
	DeliveryRetryWait  = "retry_wait"
	DeliverySucceeded  = "succeeded"
	DeliveryFailed     = "failed"
	DeliveryCancelled  = "cancelled"

	signingSecretPrefix = "va_whsec_"
	signingSecretBytes  = 32
)

var (
	ErrForbidden             = errors.New("webhook forbidden")
	ErrInvalidInput          = errors.New("invalid webhook input")
	ErrNotFound              = errors.New("webhook not found")
	ErrConflict              = errors.New("webhook conflict")
	ErrEncryptionUnavailable = errors.New("webhook encryption unavailable")
	ErrConfiguration         = errors.New("webhook configuration error")
)

var allowedEvents = map[string]struct{}{
	EventJobSucceeded: {},
	EventJobFailed:    {},
	EventJobCancelled: {},
}

type SecretCipher interface {
	Seal(plaintext, associatedData []byte) ([]byte, error)
	Open(ciphertext, associatedData []byte) ([]byte, error)
}

type Endpoint struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	DisplayName      string    `json:"display_name"`
	URL              string    `json:"url"`
	EventTypes       []string  `json:"event_types"`
	State            string    `json:"state"`
	Version          int64     `json:"version"`
	SecretConfigured bool      `json:"secret_configured"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type CreateInput struct {
	DisplayName string   `json:"display_name"`
	URL         string   `json:"url"`
	EventTypes  []string `json:"event_types"`
	State       string   `json:"state,omitempty"`
}

type CreateResult struct {
	Endpoint
	SigningSecret string `json:"signing_secret"`
}

type CreateParams struct {
	EndpointID       string
	AuditID          string
	WorkspaceID      string
	CreatedBy        string
	RequestID        string
	DisplayName      string
	URL              string
	EventTypes       []string
	State            string
	Version          int64
	SecretCiphertext []byte
}

type UpdateInput struct {
	DisplayName *string   `json:"display_name,omitempty"`
	URL         *string   `json:"url,omitempty"`
	EventTypes  *[]string `json:"event_types,omitempty"`
	State       *string   `json:"state,omitempty"`
}

type UpdateParams struct {
	EndpointID      string
	AuditID         string
	WorkspaceID     string
	UpdatedBy       string
	RequestID       string
	DisplayName     string
	URL             string
	EventTypes      []string
	State           string
	ExpectedVersion int64
}

type RotateSecretParams struct {
	EndpointID       string
	AuditID          string
	WorkspaceID      string
	UpdatedBy        string
	RequestID        string
	ExpectedVersion  int64
	SecretVersion    int64
	SecretCiphertext []byte
}

type EnqueueTestParams struct {
	DeliveryID     string
	EventID        string
	AuditID        string
	WorkspaceID    string
	WebhookID      string
	WebhookVersion int64
	RequestID      string
	CreatedBy      string
	CreatedAt      time.Time
	Payload        []byte
}

type StoredEndpoint struct {
	Endpoint
	SecretVersion    int64
	SecretCiphertext []byte
}

type Delivery struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	WebhookID      string     `json:"webhook_id"`
	WebhookVersion int64      `json:"webhook_version"`
	NotificationID *string    `json:"notification_id,omitempty"`
	EventID        string     `json:"event_id"`
	EventType      string     `json:"event_type"`
	Payload        []byte     `json:"-"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	MaxAttempts    int        `json:"max_attempts"`
	AvailableAt    time.Time  `json:"available_at"`
	ResponseStatus *int       `json:"response_status,omitempty"`
	LastErrorCode  *string    `json:"last_error_code,omitempty"`
	DeliveredAt    *time.Time `json:"delivered_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type Repository interface {
	Create(context.Context, CreateParams) (Endpoint, error)
	List(context.Context, string) ([]Endpoint, error)
	ListDeliveries(context.Context, string, string, int) ([]Delivery, error)
	GetStored(context.Context, string, string) (StoredEndpoint, error)
	Update(context.Context, UpdateParams) (Endpoint, error)
	RotateSecret(context.Context, RotateSecretParams) (Endpoint, error)
	EnqueueTest(context.Context, EnqueueTestParams) (Delivery, error)
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
	requestID string,
) (CreateResult, error) {
	workspaceID, actorID, err := authorizeOwnerSession(principal, auth.ScopeAdminWrite)
	if err != nil {
		return CreateResult{}, err
	}
	if service == nil || service.repository == nil || service.random == nil || service.now == nil ||
		requestID == "" || len(requestID) > 200 {
		return CreateResult{}, ErrInvalidInput
	}
	displayName := strings.TrimSpace(input.DisplayName)
	state := strings.TrimSpace(input.State)
	if state == "" {
		state = StateDisabled
	}
	endpointURL, urlErr := outboundhttp.ParsePublicHTTPSURL(input.URL)
	events, eventsOK := normalizeEvents(input.EventTypes)
	if !validDisplayName(displayName) || (state != StateEnabled && state != StateDisabled) ||
		urlErr != nil || !eventsOK {
		return CreateResult{}, ErrInvalidInput
	}
	if service.cipher == nil {
		return CreateResult{}, ErrEncryptionUnavailable
	}

	endpointID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return CreateResult{}, fmt.Errorf("generate webhook identifier: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return CreateResult{}, fmt.Errorf("generate webhook audit identifier: %w", err)
	}
	secret, err := generateSigningSecret(service.random)
	if err != nil {
		return CreateResult{}, fmt.Errorf("generate webhook signing secret: %w", err)
	}
	secretBytes := []byte(secret)
	defer clear(secretBytes)
	ciphertext, err := service.cipher.Seal(
		secretBytes,
		secretAssociatedData(workspaceID, endpointID, 1),
	)
	if err != nil {
		return CreateResult{}, ErrConfiguration
	}
	created, err := service.repository.Create(ctx, CreateParams{
		EndpointID: endpointID, AuditID: auditID, WorkspaceID: workspaceID,
		CreatedBy: actorID, RequestID: requestID, DisplayName: displayName,
		URL: endpointURL.String(), EventTypes: events, State: state, Version: 1,
		SecretCiphertext: ciphertext,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return CreateResult{}, ErrConflict
		}
		return CreateResult{}, fmt.Errorf("create webhook: %w", err)
	}
	return CreateResult{Endpoint: created, SigningSecret: secret}, nil
}

func (service *Service) List(ctx context.Context, principal auth.Principal) ([]Endpoint, error) {
	workspaceID, _, err := authorizeOwnerSession(principal, auth.ScopeAdminRead)
	if err != nil {
		return nil, err
	}
	if service == nil || service.repository == nil {
		return nil, ErrConfiguration
	}
	items, err := service.repository.List(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list webhooks: %w", err)
	}
	if items == nil {
		items = make([]Endpoint, 0)
	}
	return items, nil
}

func (service *Service) ListDeliveries(
	ctx context.Context,
	principal auth.Principal,
	endpointID string,
	limit int,
) ([]Delivery, error) {
	workspaceID, _, err := authorizeOwnerSession(principal, auth.ScopeAdminRead)
	if err != nil {
		return nil, err
	}
	if service == nil || service.repository == nil {
		return nil, ErrConfiguration
	}
	endpointID, validID := identifier.NormalizeUUID(endpointID)
	if !validID || limit < 0 || limit > 100 {
		return nil, ErrInvalidInput
	}
	if limit == 0 {
		limit = 50
	}
	if _, err := service.repository.GetStored(ctx, workspaceID, endpointID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load webhook delivery target: %w", err)
	}
	items, err := service.repository.ListDeliveries(ctx, workspaceID, endpointID, limit)
	if err != nil {
		return nil, fmt.Errorf("list webhook deliveries: %w", err)
	}
	if items == nil {
		items = make([]Delivery, 0)
	}
	return items, nil
}

func (service *Service) Update(
	ctx context.Context,
	principal auth.Principal,
	endpointID string,
	expectedVersion int64,
	input UpdateInput,
	requestID string,
) (Endpoint, error) {
	workspaceID, actorID, err := authorizeOwnerSession(principal, auth.ScopeAdminWrite)
	if err != nil {
		return Endpoint{}, err
	}
	if service == nil || service.repository == nil || service.random == nil ||
		expectedVersion < 1 || !validRequestID(requestID) || !hasUpdate(input) {
		return Endpoint{}, ErrInvalidInput
	}
	endpointID, validID := identifier.NormalizeUUID(endpointID)
	if !validID {
		return Endpoint{}, ErrInvalidInput
	}
	stored, err := service.repository.GetStored(ctx, workspaceID, endpointID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Endpoint{}, ErrNotFound
		}
		return Endpoint{}, fmt.Errorf("load webhook: %w", err)
	}
	if stored.Version != expectedVersion {
		return Endpoint{}, ErrConflict
	}
	displayName := stored.DisplayName
	if input.DisplayName != nil {
		displayName = strings.TrimSpace(*input.DisplayName)
	}
	endpointURL := stored.URL
	if input.URL != nil {
		parsed, parseErr := outboundhttp.ParsePublicHTTPSURL(*input.URL)
		if parseErr != nil {
			return Endpoint{}, ErrInvalidInput
		}
		endpointURL = parsed.String()
	}
	events := append([]string(nil), stored.EventTypes...)
	if input.EventTypes != nil {
		var eventsOK bool
		events, eventsOK = normalizeEvents(*input.EventTypes)
		if !eventsOK {
			return Endpoint{}, ErrInvalidInput
		}
	}
	state := stored.State
	if input.State != nil {
		state = strings.TrimSpace(*input.State)
	}
	if !validDisplayName(displayName) || (state != StateEnabled && state != StateDisabled) {
		return Endpoint{}, ErrInvalidInput
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Endpoint{}, fmt.Errorf("generate webhook audit identifier: %w", err)
	}
	updated, err := service.repository.Update(ctx, UpdateParams{
		EndpointID: endpointID, AuditID: auditID, WorkspaceID: workspaceID,
		UpdatedBy: actorID, RequestID: requestID, DisplayName: displayName,
		URL: endpointURL, EventTypes: events, State: state,
		ExpectedVersion: expectedVersion,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return Endpoint{}, ErrConflict
		}
		if errors.Is(err, ErrNotFound) {
			return Endpoint{}, ErrNotFound
		}
		return Endpoint{}, fmt.Errorf("update webhook: %w", err)
	}
	return updated, nil
}

func (service *Service) RotateSecret(
	ctx context.Context,
	principal auth.Principal,
	endpointID string,
	expectedVersion int64,
	requestID string,
) (CreateResult, error) {
	workspaceID, actorID, err := authorizeOwnerSession(principal, auth.ScopeAdminWrite)
	if err != nil {
		return CreateResult{}, err
	}
	if service == nil || service.repository == nil || service.cipher == nil ||
		service.random == nil || expectedVersion < 1 || !validRequestID(requestID) {
		if service != nil && service.cipher == nil {
			return CreateResult{}, ErrEncryptionUnavailable
		}
		return CreateResult{}, ErrInvalidInput
	}
	endpointID, validID := identifier.NormalizeUUID(endpointID)
	if !validID {
		return CreateResult{}, ErrInvalidInput
	}
	stored, err := service.repository.GetStored(ctx, workspaceID, endpointID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CreateResult{}, ErrNotFound
		}
		return CreateResult{}, fmt.Errorf("load webhook for secret rotation: %w", err)
	}
	if stored.Version != expectedVersion {
		return CreateResult{}, ErrConflict
	}
	secret, err := generateSigningSecret(service.random)
	if err != nil {
		return CreateResult{}, fmt.Errorf("generate rotated webhook secret: %w", err)
	}
	secretBytes := []byte(secret)
	defer clear(secretBytes)
	secretVersion := stored.SecretVersion + 1
	if secretVersion < 1 {
		return CreateResult{}, ErrConfiguration
	}
	ciphertext, err := service.cipher.Seal(
		secretBytes,
		secretAssociatedData(workspaceID, endpointID, secretVersion),
	)
	if err != nil {
		return CreateResult{}, ErrConfiguration
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return CreateResult{}, fmt.Errorf("generate webhook rotation audit identifier: %w", err)
	}
	updated, err := service.repository.RotateSecret(ctx, RotateSecretParams{
		EndpointID: endpointID, AuditID: auditID, WorkspaceID: workspaceID,
		UpdatedBy: actorID, RequestID: requestID, ExpectedVersion: expectedVersion,
		SecretVersion: secretVersion, SecretCiphertext: ciphertext,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return CreateResult{}, ErrConflict
		}
		if errors.Is(err, ErrNotFound) {
			return CreateResult{}, ErrNotFound
		}
		return CreateResult{}, fmt.Errorf("rotate webhook secret: %w", err)
	}
	return CreateResult{Endpoint: updated, SigningSecret: secret}, nil
}

func (service *Service) EnqueueTest(
	ctx context.Context,
	principal auth.Principal,
	endpointID string,
	requestID string,
) (Delivery, error) {
	workspaceID, actorID, err := authorizeOwnerSession(principal, auth.ScopeAdminWrite)
	if err != nil {
		return Delivery{}, err
	}
	if service == nil || service.repository == nil || service.random == nil ||
		service.now == nil || !validRequestID(requestID) {
		return Delivery{}, ErrInvalidInput
	}
	endpointID, validID := identifier.NormalizeUUID(endpointID)
	if !validID {
		return Delivery{}, ErrInvalidInput
	}
	stored, err := service.repository.GetStored(ctx, workspaceID, endpointID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Delivery{}, ErrNotFound
		}
		return Delivery{}, fmt.Errorf("load webhook test target: %w", err)
	}
	if stored.State != StateEnabled || stored.SecretVersion < 1 || len(stored.SecretCiphertext) == 0 {
		return Delivery{}, ErrConflict
	}
	deliveryID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Delivery{}, fmt.Errorf("generate webhook delivery identifier: %w", err)
	}
	eventID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Delivery{}, fmt.Errorf("generate webhook event identifier: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Delivery{}, fmt.Errorf("generate webhook test audit identifier: %w", err)
	}
	createdAt := service.now().UTC()
	payload, err := json.Marshal(struct {
		ID        string    `json:"id"`
		Type      string    `json:"type"`
		CreatedAt time.Time `json:"created_at"`
		Data      struct {
			Webhook struct {
				ID string `json:"id"`
			} `json:"webhook"`
		} `json:"data"`
	}{
		ID: eventID, Type: EventWebhookTest, CreatedAt: createdAt,
		Data: struct {
			Webhook struct {
				ID string `json:"id"`
			} `json:"webhook"`
		}{Webhook: struct {
			ID string `json:"id"`
		}{ID: endpointID}},
	})
	if err != nil {
		return Delivery{}, ErrConfiguration
	}
	return service.repository.EnqueueTest(ctx, EnqueueTestParams{
		DeliveryID: deliveryID, EventID: eventID, AuditID: auditID, WorkspaceID: workspaceID,
		WebhookID: endpointID, WebhookVersion: stored.Version, RequestID: requestID,
		CreatedBy: actorID, CreatedAt: createdAt, Payload: payload,
	})
}

func authorizeOwnerSession(principal auth.Principal, scope string) (string, string, error) {
	if principal.CredentialType != "session" || principal.Role != "owner" || !principal.Can(scope) {
		return "", "", ErrForbidden
	}
	workspaceID, workspaceOK := identifier.NormalizeUUID(principal.WorkspaceID)
	actorID, actorOK := identifier.NormalizeUUID(principal.UserID)
	if !workspaceOK || !actorOK {
		return "", "", ErrForbidden
	}
	return workspaceID, actorID, nil
}

func normalizeEvents(values []string) ([]string, bool) {
	if len(values) == 0 || len(values) > len(allowedEvents) {
		return nil, false
	}
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := allowedEvents[value]; !ok {
			return nil, false
		}
		unique[value] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result, len(result) > 0
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

func hasUpdate(input UpdateInput) bool {
	return input.DisplayName != nil || input.URL != nil || input.EventTypes != nil || input.State != nil
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 200 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func generateSigningSecret(random io.Reader) (string, error) {
	value := make([]byte, signingSecretBytes)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	defer clear(value)
	return signingSecretPrefix + base64.RawURLEncoding.EncodeToString(value), nil
}

func secretAssociatedData(workspaceID, endpointID string, version int64) []byte {
	return []byte(fmt.Sprintf(
		"voiceasset/webhook-secret/v1\x00%s\x00%s\x00%d",
		workspaceID, endpointID, version,
	))
}
