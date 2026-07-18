package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const (
	defaultSessionTTL  = 12 * time.Hour
	defaultRefreshTTL  = 30 * 24 * time.Hour
	defaultPairingTTL  = 5 * time.Minute
	refreshTokenPrefix = "va_rft_"
	refreshTokenLength = len(refreshTokenPrefix) + 43
	pairingTokenPrefix = "va_pair_"
	pairingTokenLength = len(pairingTokenPrefix) + 43
)

const (
	APIKeyTokenPrefix = "va_pat_"
	apiKeyTokenLength = len(APIKeyTokenPrefix) + 43
)

var (
	ErrAccountNotFound       = errors.New("account not found")
	ErrSessionNotFound       = errors.New("session not found")
	ErrInvalidCredentials    = errors.New("invalid credentials")
	ErrUnauthorized          = errors.New("unauthorized")
	ErrInvalidDeviceName     = errors.New("invalid device name")
	ErrDeviceSessionNotFound = errors.New("device session not found")
	ErrInvalidPairing        = errors.New("invalid pairing session")
	ErrPairingUnavailable    = errors.New("pairing session unavailable")
)

type LoginAccount struct {
	UserID       string
	WorkspaceID  string
	Role         string
	Email        string
	PasswordHash string
	Status       string
}

type Principal struct {
	UserID         string   `json:"id"`
	WorkspaceID    string   `json:"workspace_id"`
	Role           string   `json:"role"`
	Email          string   `json:"email"`
	Scopes         []string `json:"scopes"`
	CredentialType string   `json:"-"`
	CredentialID   string   `json:"-"`
}

type NewSession struct {
	ID               string
	AuditID          string
	UserID           string
	WorkspaceID      string
	TokenHash        string
	RefreshTokenHash string
	DeviceName       string
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
}

type RotateSessionParams struct {
	AuditID                 string
	CurrentRefreshTokenHash string
	NewTokenHash            string
	NewRefreshTokenHash     string
	NewExpiresAt            time.Time
	RotatedAt               time.Time
}

type SessionIdentity struct {
	ID               string
	Principal        Principal
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
}

type DeviceSession struct {
	ID               string     `json:"id"`
	DeviceName       string     `json:"device_name"`
	Current          bool       `json:"current"`
	CreatedAt        time.Time  `json:"created_at"`
	LastSeenAt       time.Time  `json:"last_seen_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	RefreshExpiresAt time.Time  `json:"refresh_expires_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
}

type RevokeDeviceSessionParams struct {
	AuditID     string
	WorkspaceID string
	UserID      string
	SessionID   string
	RevokedAt   time.Time
}

type PairingSession struct {
	ID        string
	Secret    string
	ExpiresAt time.Time
}

type CreatePairingSessionParams struct {
	PairingSessionID string
	AuditID          string
	WorkspaceID      string
	UserID           string
	SecretHash       string
	CreatedAt        time.Time
	ExpiresAt        time.Time
}

type ClaimPairingSessionParams struct {
	PairingSessionID string
	SessionID        string
	SessionAuditID   string
	ClaimAuditID     string
	SecretHash       string
	TokenHash        string
	RefreshTokenHash string
	DeviceName       string
	ClaimedAt        time.Time
	ExpiresAt        time.Time
	RefreshExpiresAt time.Time
}

type PairingIdentity struct {
	SessionID   string
	UserID      string
	WorkspaceID string
	Role        string
	Email       string
}

type PairingRepository interface {
	CreatePairingSession(context.Context, CreatePairingSessionParams) error
	ClaimPairingSession(context.Context, ClaimPairingSessionParams) (PairingIdentity, error)
}

type SessionLifecycleRepository interface {
	RotateSession(context.Context, RotateSessionParams) (SessionIdentity, error)
	ListDeviceSessions(context.Context, string, string, time.Time) ([]DeviceSession, error)
	RevokeDeviceSession(context.Context, RevokeDeviceSessionParams) (DeviceSession, error)
}

type AuditedSessionRevoker interface {
	RevokeSessionWithAudit(context.Context, string, string, time.Time) error
}

type Repository interface {
	FindLoginAccount(ctx context.Context, email string) (LoginAccount, error)
	CreateSession(ctx context.Context, session NewSession) error
	ResolveSession(ctx context.Context, tokenHash string, now time.Time) (Principal, error)
	RevokeSession(ctx context.Context, tokenHash string, now time.Time) error
}

type APIKeyResolver interface {
	ResolveAPIKey(ctx context.Context, tokenHash string, now time.Time) (Principal, error)
}

type LoginResult struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"-"`
	TokenType        string    `json:"token_type"`
	ExpiresAt        time.Time `json:"expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
	User             Principal `json:"user"`
}

type Service struct {
	repository Repository
	hasher     PasswordHasher
	random     io.Reader
	now        func() time.Time
	sessionTTL time.Duration
	dummyHash  string
}

func NewService(repository Repository, hasher PasswordHasher) *Service {
	dummyHash, _ := hasher.Hash("voiceasset-invalid-password-sentinel")
	return &Service{
		repository: repository,
		hasher:     hasher,
		random:     rand.Reader,
		now:        time.Now,
		sessionTTL: defaultSessionTTL,
		dummyHash:  dummyHash,
	}
}

func (s *Service) Login(ctx context.Context, email, password string) (LoginResult, error) {
	return s.LoginWithDevice(ctx, email, password, "Web session")
}

func (s *Service) LoginWithDevice(
	ctx context.Context,
	email,
	password,
	deviceName string,
) (LoginResult, error) {
	normalizedEmail, ok := normalizeEmail(email)
	if !ok || password == "" {
		return LoginResult{}, ErrInvalidCredentials
	}
	deviceName, ok = normalizeDeviceName(deviceName)
	if !ok {
		return LoginResult{}, ErrInvalidDeviceName
	}
	account, err := s.repository.FindLoginAccount(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			_, _ = s.hasher.Verify(s.dummyHash, password)
			return LoginResult{}, ErrInvalidCredentials
		}
		return LoginResult{}, fmt.Errorf("find login account: %w", err)
	}
	matched, err := s.hasher.Verify(account.PasswordHash, password)
	if err != nil {
		return LoginResult{}, fmt.Errorf("verify password: %w", err)
	}
	if !matched || account.Status != "active" {
		return LoginResult{}, ErrInvalidCredentials
	}

	token, err := randomToken(s.random, "va_")
	if err != nil {
		return LoginResult{}, err
	}
	refreshToken, err := randomToken(s.random, refreshTokenPrefix)
	if err != nil {
		return LoginResult{}, err
	}
	sessionID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return LoginResult{}, err
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.sessionTTL)
	refreshExpiresAt := now.Add(defaultRefreshTTL)
	if err := s.repository.CreateSession(ctx, NewSession{
		ID: sessionID, AuditID: auditID, UserID: account.UserID, WorkspaceID: account.WorkspaceID,
		TokenHash: tokenDigest(token), RefreshTokenHash: tokenDigest(refreshToken),
		DeviceName: deviceName, ExpiresAt: expiresAt, RefreshExpiresAt: refreshExpiresAt,
	}); err != nil {
		return LoginResult{}, fmt.Errorf("create session: %w", err)
	}

	return LoginResult{
		AccessToken: token, RefreshToken: refreshToken, TokenType: "Bearer",
		ExpiresAt: expiresAt, RefreshExpiresAt: refreshExpiresAt,
		User: Principal{
			UserID:         account.UserID,
			WorkspaceID:    account.WorkspaceID,
			Role:           account.Role,
			Email:          account.Email,
			Scopes:         ScopesForRole(account.Role),
			CredentialType: "session", CredentialID: sessionID,
		},
	}, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken string) (LoginResult, error) {
	if len(refreshToken) != refreshTokenLength || !strings.HasPrefix(refreshToken, refreshTokenPrefix) {
		return LoginResult{}, ErrUnauthorized
	}
	repository, ok := s.repository.(SessionLifecycleRepository)
	if !ok {
		return LoginResult{}, ErrUnauthorized
	}
	newToken, err := randomToken(s.random, "va_")
	if err != nil {
		return LoginResult{}, err
	}
	newRefreshToken, err := randomToken(s.random, refreshTokenPrefix)
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now().UTC()
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return LoginResult{}, err
	}
	identity, err := repository.RotateSession(ctx, RotateSessionParams{
		AuditID:                 auditID,
		CurrentRefreshTokenHash: tokenDigest(refreshToken),
		NewTokenHash:            tokenDigest(newToken), NewRefreshTokenHash: tokenDigest(newRefreshToken),
		NewExpiresAt: now.Add(s.sessionTTL), RotatedAt: now,
	})
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return LoginResult{}, ErrUnauthorized
		}
		return LoginResult{}, fmt.Errorf("rotate session: %w", err)
	}
	if !identity.RefreshExpiresAt.After(now) {
		return LoginResult{}, ErrUnauthorized
	}
	expiresAt := now.Add(s.sessionTTL)
	if !identity.ExpiresAt.IsZero() {
		expiresAt = identity.ExpiresAt
	} else if identity.RefreshExpiresAt.Before(expiresAt) {
		expiresAt = identity.RefreshExpiresAt
	}
	identity.Principal.Scopes = ScopesForRole(identity.Principal.Role)
	identity.Principal.CredentialType = "session"
	identity.Principal.CredentialID = identity.ID
	return LoginResult{
		AccessToken: newToken, RefreshToken: newRefreshToken, TokenType: "Bearer",
		ExpiresAt: expiresAt, RefreshExpiresAt: identity.RefreshExpiresAt,
		User: identity.Principal,
	}, nil
}

func (s *Service) ListDeviceSessions(
	ctx context.Context,
	principal Principal,
) ([]DeviceSession, error) {
	repository, ok := s.repository.(SessionLifecycleRepository)
	workspaceID, workspaceOK := identifier.NormalizeUUID(principal.WorkspaceID)
	userID, userOK := identifier.NormalizeUUID(principal.UserID)
	if !ok || !workspaceOK || !userOK || principal.CredentialType != "session" {
		return nil, ErrUnauthorized
	}
	sessions, err := repository.ListDeviceSessions(ctx, workspaceID, userID, s.now().UTC())
	if err != nil {
		return nil, fmt.Errorf("list device sessions: %w", err)
	}
	for index := range sessions {
		sessions[index].Current = sessions[index].ID == principal.CredentialID
	}
	return sessions, nil
}

func (s *Service) RevokeDeviceSession(
	ctx context.Context,
	principal Principal,
	sessionID string,
) (DeviceSession, error) {
	repository, ok := s.repository.(SessionLifecycleRepository)
	workspaceID, workspaceOK := identifier.NormalizeUUID(principal.WorkspaceID)
	userID, userOK := identifier.NormalizeUUID(principal.UserID)
	sessionID, sessionOK := identifier.NormalizeUUID(sessionID)
	if !ok || !workspaceOK || !userOK || !sessionOK || principal.CredentialType != "session" {
		return DeviceSession{}, ErrUnauthorized
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return DeviceSession{}, err
	}
	revoked, err := repository.RevokeDeviceSession(ctx, RevokeDeviceSessionParams{
		AuditID: auditID, WorkspaceID: workspaceID, UserID: userID,
		SessionID: sessionID, RevokedAt: s.now().UTC(),
	})
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return DeviceSession{}, ErrDeviceSessionNotFound
		}
		return DeviceSession{}, fmt.Errorf("revoke device session: %w", err)
	}
	revoked.Current = revoked.ID == principal.CredentialID
	return revoked, nil
}

func (s *Service) CreatePairingSession(
	ctx context.Context,
	principal Principal,
) (PairingSession, error) {
	repository, ok := s.repository.(PairingRepository)
	workspaceID, workspaceOK := identifier.NormalizeUUID(principal.WorkspaceID)
	userID, userOK := identifier.NormalizeUUID(principal.UserID)
	if !ok || !workspaceOK || !userOK || principal.CredentialType != "session" {
		return PairingSession{}, ErrUnauthorized
	}
	secret, err := randomToken(s.random, pairingTokenPrefix)
	if err != nil {
		return PairingSession{}, err
	}
	pairingID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return PairingSession{}, err
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return PairingSession{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(defaultPairingTTL)
	if err := repository.CreatePairingSession(ctx, CreatePairingSessionParams{
		PairingSessionID: pairingID,
		AuditID:          auditID,
		WorkspaceID:      workspaceID,
		UserID:           userID,
		SecretHash:       tokenDigest(secret),
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
	}); err != nil {
		return PairingSession{}, fmt.Errorf("create pairing session: %w", err)
	}
	return PairingSession{ID: pairingID, Secret: secret, ExpiresAt: expiresAt}, nil
}

func (s *Service) ClaimPairingSession(
	ctx context.Context,
	pairingID,
	secret,
	deviceName string,
) (LoginResult, error) {
	repository, available := s.repository.(PairingRepository)
	pairingID, validID := identifier.NormalizeUUID(pairingID)
	deviceName, validDevice := normalizeDeviceName(deviceName)
	if !available || !validID || !validDevice ||
		len(secret) != pairingTokenLength || !strings.HasPrefix(secret, pairingTokenPrefix) {
		return LoginResult{}, ErrInvalidPairing
	}
	token, err := randomToken(s.random, "va_")
	if err != nil {
		return LoginResult{}, err
	}
	refreshToken, err := randomToken(s.random, refreshTokenPrefix)
	if err != nil {
		return LoginResult{}, err
	}
	sessionID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return LoginResult{}, err
	}
	sessionAuditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return LoginResult{}, err
	}
	claimAuditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.sessionTTL)
	refreshExpiresAt := now.Add(defaultRefreshTTL)
	identity, err := repository.ClaimPairingSession(ctx, ClaimPairingSessionParams{
		PairingSessionID: pairingID,
		SessionID:        sessionID,
		SessionAuditID:   sessionAuditID,
		ClaimAuditID:     claimAuditID,
		SecretHash:       tokenDigest(secret),
		TokenHash:        tokenDigest(token),
		RefreshTokenHash: tokenDigest(refreshToken),
		DeviceName:       deviceName,
		ClaimedAt:        now,
		ExpiresAt:        expiresAt,
		RefreshExpiresAt: refreshExpiresAt,
	})
	if err != nil {
		if errors.Is(err, ErrPairingUnavailable) {
			return LoginResult{}, ErrInvalidPairing
		}
		return LoginResult{}, fmt.Errorf("claim pairing session: %w", err)
	}
	return LoginResult{
		AccessToken: token, RefreshToken: refreshToken, TokenType: "Bearer",
		ExpiresAt: expiresAt, RefreshExpiresAt: refreshExpiresAt,
		User: Principal{
			UserID: identity.UserID, WorkspaceID: identity.WorkspaceID,
			Role: identity.Role, Email: identity.Email, Scopes: ScopesForRole(identity.Role),
			CredentialType: "session", CredentialID: identity.SessionID,
		},
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if strings.HasPrefix(token, APIKeyTokenPrefix) {
		if len(token) != apiKeyTokenLength {
			return Principal{}, ErrUnauthorized
		}
		resolver, ok := s.repository.(APIKeyResolver)
		if !ok {
			return Principal{}, ErrUnauthorized
		}
		principal, err := resolver.ResolveAPIKey(ctx, tokenDigest(token), s.now().UTC())
		if err != nil {
			if errors.Is(err, ErrSessionNotFound) {
				return Principal{}, ErrUnauthorized
			}
			return Principal{}, fmt.Errorf("resolve API key: %w", err)
		}
		return principal, nil
	}
	if !strings.HasPrefix(token, "va_") || len(token) < 20 {
		return Principal{}, ErrUnauthorized
	}
	principal, err := s.repository.ResolveSession(ctx, tokenDigest(token), s.now().UTC())
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return Principal{}, ErrUnauthorized
		}
		return Principal{}, fmt.Errorf("resolve session: %w", err)
	}
	return principal, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if strings.HasPrefix(token, APIKeyTokenPrefix) {
		return ErrUnauthorized
	}
	if !strings.HasPrefix(token, "va_") || len(token) < 20 {
		return ErrUnauthorized
	}
	now := s.now().UTC()
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return err
	}
	if revoker, ok := s.repository.(AuditedSessionRevoker); ok {
		err = revoker.RevokeSessionWithAudit(ctx, tokenDigest(token), auditID, now)
	} else {
		err = s.repository.RevokeSession(ctx, tokenDigest(token), now)
	}
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func normalizeEmail(value string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(value))
	if len(email) < 3 || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		return "", false
	}
	separator := strings.LastIndexByte(email, '@')
	if separator <= 0 || separator == len(email)-1 || strings.Contains(email[separator+1:], "@") {
		return "", false
	}
	return email, true
}

func randomToken(random io.Reader, prefix string) (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("generate access token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func normalizeDeviceName(value string) (string, bool) {
	if !utf8.ValidString(value) {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = "Web session"
	}
	if len(value) > 400 || len([]rune(value)) > 100 {
		return "", false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", false
		}
	}
	return value, true
}
