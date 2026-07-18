// Package apikey manages hashed, scoped credentials for Agents and integrations.
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
)

const (
	minTTL = 5 * time.Minute
	maxTTL = 365 * 24 * time.Hour
)

var (
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid API key input")
	ErrNotFound     = errors.New("API key not found")
)

type APIKey struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	Name        string     `json:"name"`
	TokenPrefix string     `json:"token_prefix"`
	Scopes      []string   `json:"scopes"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type CreateInput struct {
	Name      string    `json:"name"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CreateResult struct {
	APIKey APIKey `json:"api_key"`
	Token  string `json:"token"`
}

type CreateParams struct {
	ID          string
	AuditID     string
	WorkspaceID string
	CreatedBy   string
	ActorType   string
	Name        string
	TokenPrefix string
	TokenHash   string
	Scopes      []string
	ExpiresAt   time.Time
	RequestID   string
}

type RevokeParams struct {
	ID          string
	AuditID     string
	WorkspaceID string
	ActorID     string
	ActorType   string
	RevokedAt   time.Time
	RequestID   string
}

type Repository interface {
	Create(context.Context, CreateParams) (APIKey, error)
	List(context.Context, string) ([]APIKey, error)
	Revoke(context.Context, RevokeParams) (APIKey, error)
}

type Service struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader, now: time.Now}
}

func (service *Service) Create(
	ctx context.Context,
	principal auth.Principal,
	input CreateInput,
	requestID string,
) (CreateResult, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return CreateResult{}, ErrForbidden
	}
	name := strings.TrimSpace(input.Name)
	scopes, validScopes := normalizeScopes(input.Scopes, principal)
	now := service.now().UTC()
	expiresAt := input.ExpiresAt.UTC()
	if !validName(name) || !validScopes || expiresAt.Before(now.Add(minTTL)) || expiresAt.After(now.Add(maxTTL)) {
		return CreateResult{}, ErrInvalidInput
	}
	keyID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return CreateResult{}, err
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return CreateResult{}, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(service.random, tokenBytes); err != nil {
		return CreateResult{}, fmt.Errorf("generate API key token: %w", err)
	}
	token := auth.APIKeyTokenPrefix + base64.RawURLEncoding.EncodeToString(tokenBytes)
	digest := sha256.Sum256([]byte(token))
	created, err := service.repository.Create(ctx, CreateParams{
		ID: keyID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		CreatedBy: principal.UserID, ActorType: actorType(principal), Name: name,
		TokenPrefix: token[:len(auth.APIKeyTokenPrefix)+8], TokenHash: hex.EncodeToString(digest[:]),
		Scopes: scopes, ExpiresAt: expiresAt, RequestID: requestID,
	})
	if err != nil {
		return CreateResult{}, fmt.Errorf("create API key: %w", err)
	}
	return CreateResult{APIKey: created, Token: token}, nil
}

func (service *Service) List(ctx context.Context, principal auth.Principal) ([]APIKey, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return nil, ErrForbidden
	}
	results, err := service.repository.List(ctx, principal.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("list API keys: %w", err)
	}
	if results == nil {
		results = make([]APIKey, 0)
	}
	return results, nil
}

func (service *Service) Revoke(
	ctx context.Context,
	principal auth.Principal,
	keyID string,
	requestID string,
) (APIKey, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return APIKey{}, ErrForbidden
	}
	keyID, validID := identifier.NormalizeUUID(keyID)
	if !validID {
		return APIKey{}, ErrNotFound
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return APIKey{}, err
	}
	result, err := service.repository.Revoke(ctx, RevokeParams{
		ID: keyID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		ActorID: principal.UserID, ActorType: actorType(principal),
		RevokedAt: service.now().UTC(), RequestID: requestID,
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return APIKey{}, ErrNotFound
		}
		return APIKey{}, fmt.Errorf("revoke API key: %w", err)
	}
	return result, nil
}

func normalizeScopes(values []string, principal auth.Principal) ([]string, bool) {
	if len(values) < 1 || len(values) > 20 {
		return nil, false
	}
	seen := make(map[string]struct{}, len(values))
	results := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !auth.ValidScope(value) || !principal.Can(value) {
			return nil, false
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, false
		}
		seen[value] = struct{}{}
		results = append(results, value)
	}
	sort.Strings(results)
	return results, true
}

func validName(value string) bool {
	length := utf8.RuneCountInString(value)
	if length < 1 || length > 100 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func actorType(principal auth.Principal) string {
	if principal.Role == "agent" || principal.CredentialType == "api_key" {
		return "agent"
	}
	return "user"
}
