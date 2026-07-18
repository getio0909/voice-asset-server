package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLoginStoresOnlyTokenDigest(t *testing.T) {
	hasher := PasswordHasher{Iterations: 1_000}
	passwordHash, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	repository := &fakeRepository{account: LoginAccount{
		UserID:       "user-1",
		WorkspaceID:  "workspace-1",
		Role:         "owner",
		Email:        "owner@example.com",
		PasswordHash: passwordHash,
		Status:       "active",
	}}
	now := time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)
	service := NewService(repository, hasher)
	service.now = func() time.Time { return now }

	result, err := service.LoginWithDevice(
		context.Background(),
		" OWNER@example.com ",
		"correct horse battery staple",
		" Pixel 9 Pro ",
	)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.AccessToken == "" {
		t.Fatal("AccessToken is empty")
	}
	if repository.session.TokenHash == result.AccessToken {
		t.Fatal("repository received the plaintext access token")
	}
	digest := sha256.Sum256([]byte(result.AccessToken))
	if repository.session.TokenHash != hex.EncodeToString(digest[:]) {
		t.Fatalf("TokenHash = %q, want SHA-256 digest", repository.session.TokenHash)
	}
	if result.RefreshToken == "" || repository.session.RefreshTokenHash == result.RefreshToken {
		t.Fatal("refresh token is empty or was stored as plaintext")
	}
	refreshDigest := sha256.Sum256([]byte(result.RefreshToken))
	if repository.session.RefreshTokenHash != hex.EncodeToString(refreshDigest[:]) {
		t.Fatalf("RefreshTokenHash = %q, want SHA-256 digest", repository.session.RefreshTokenHash)
	}
	if !repository.session.ExpiresAt.Equal(now.Add(defaultSessionTTL)) {
		t.Fatalf("ExpiresAt = %s, want %s", repository.session.ExpiresAt, now.Add(defaultSessionTTL))
	}
	if !repository.session.RefreshExpiresAt.Equal(now.Add(defaultRefreshTTL)) ||
		!result.RefreshExpiresAt.Equal(repository.session.RefreshExpiresAt) ||
		repository.session.DeviceName != "Pixel 9 Pro" {
		t.Fatalf("refresh/device session = %+v / %+v", repository.session, result)
	}
	if result.User.Email != "owner@example.com" || result.User.Role != "owner" {
		t.Fatalf("unexpected user: %+v", result.User)
	}
}

func TestNormalizeDeviceNameRejectsInvalidUTF8AndUnicodeControls(t *testing.T) {
	for _, value := range []string{"invalid \xff UTF-8", "next-line\u0085control"} {
		if normalized, ok := normalizeDeviceName(value); ok {
			t.Fatalf("normalizeDeviceName(%q) = %q, want rejection", value, normalized)
		}
	}

	if normalized, ok := normalizeDeviceName("  浏览器 🔐  "); !ok || normalized != "浏览器 🔐" {
		t.Fatalf("normalizeDeviceName() = %q, %v", normalized, ok)
	}
}

func TestRefreshRotatesBothTokenDigestsAndPreservesSessionFamily(t *testing.T) {
	now := time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)
	repository := &fakeRepository{rotatedSession: SessionIdentity{
		ID: "30000000-0000-4000-8000-000000000001",
		Principal: Principal{
			UserID:      "20000000-0000-4000-8000-000000000001",
			WorkspaceID: "10000000-0000-4000-8000-000000000001",
			Role:        "owner", Email: "owner@example.com",
		},
		RefreshExpiresAt: now.Add(10 * 24 * time.Hour),
	}}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})
	service.now = func() time.Time { return now }
	oldRefreshToken := refreshTokenPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	result, err := service.Refresh(context.Background(), oldRefreshToken)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	oldDigest := sha256.Sum256([]byte(oldRefreshToken))
	if repository.rotate.CurrentRefreshTokenHash != hex.EncodeToString(oldDigest[:]) {
		t.Fatalf("current refresh digest = %q", repository.rotate.CurrentRefreshTokenHash)
	}
	accessDigest := sha256.Sum256([]byte(result.AccessToken))
	refreshDigest := sha256.Sum256([]byte(result.RefreshToken))
	if repository.rotate.NewTokenHash != hex.EncodeToString(accessDigest[:]) ||
		repository.rotate.NewRefreshTokenHash != hex.EncodeToString(refreshDigest[:]) ||
		repository.rotate.NewTokenHash == result.AccessToken ||
		repository.rotate.NewRefreshTokenHash == result.RefreshToken {
		t.Fatalf("rotation stored plaintext or wrong digests: %+v", repository.rotate)
	}
	if result.User.CredentialID != repository.rotatedSession.ID ||
		result.User.CredentialType != "session" ||
		!result.ExpiresAt.Equal(now.Add(defaultSessionTTL)) ||
		!result.RefreshExpiresAt.Equal(repository.rotatedSession.RefreshExpiresAt) {
		t.Fatalf("refresh result = %+v", result)
	}
}

func TestDeviceSessionsArePersonalAndCurrentSessionIsMarked(t *testing.T) {
	const (
		workspaceID = "10000000-0000-4000-8000-000000000001"
		userID      = "20000000-0000-4000-8000-000000000001"
		currentID   = "30000000-0000-4000-8000-000000000001"
		otherID     = "30000000-0000-4000-8000-000000000002"
	)
	repository := &fakeRepository{deviceSessions: []DeviceSession{
		{ID: currentID, DeviceName: "Firefox"},
		{ID: otherID, DeviceName: "Pixel"},
	}}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})
	principal := Principal{
		UserID: userID, WorkspaceID: workspaceID,
		CredentialType: "session", CredentialID: currentID,
	}

	sessions, err := service.ListDeviceSessions(context.Background(), principal)
	if err != nil {
		t.Fatalf("ListDeviceSessions() error = %v", err)
	}
	if repository.listUserID != userID || repository.listWorkspaceID != workspaceID ||
		len(sessions) != 2 || !sessions[0].Current || sessions[1].Current {
		t.Fatalf("device sessions/repository scope = %+v / %q/%q", sessions, repository.listWorkspaceID, repository.listUserID)
	}
	if _, err := service.ListDeviceSessions(context.Background(), Principal{
		UserID: userID, WorkspaceID: workspaceID, CredentialType: "api_key",
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("API-key ListDeviceSessions() error = %v, want ErrUnauthorized", err)
	}
}

func TestRevokeDeviceSessionUsesPersonalWorkspaceScope(t *testing.T) {
	const (
		workspaceID = "10000000-0000-4000-8000-000000000001"
		userID      = "20000000-0000-4000-8000-000000000001"
		sessionID   = "30000000-0000-4000-8000-000000000002"
	)
	now := time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)
	repository := &fakeRepository{revokedDeviceSession: DeviceSession{ID: sessionID, DeviceName: "Pixel"}}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})
	service.now = func() time.Time { return now }
	principal := Principal{
		UserID: userID, WorkspaceID: workspaceID,
		CredentialType: "session", CredentialID: sessionID,
	}

	revoked, err := service.RevokeDeviceSession(context.Background(), principal, sessionID)
	if err != nil {
		t.Fatalf("RevokeDeviceSession() error = %v", err)
	}
	if repository.revokeDevice.WorkspaceID != workspaceID || repository.revokeDevice.UserID != userID ||
		repository.revokeDevice.SessionID != sessionID || !repository.revokeDevice.RevokedAt.Equal(now) ||
		!revoked.Current {
		t.Fatalf("revoke params/result = %+v / %+v", repository.revokeDevice, revoked)
	}
}

func TestCreatePairingSessionStoresOnlyDigestAndRequiresPersonalSession(t *testing.T) {
	const (
		workspaceID = "10000000-0000-4000-8000-000000000001"
		userID      = "20000000-0000-4000-8000-000000000001"
	)
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	repository := &fakePairingRepository{fakeRepository: &fakeRepository{}}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})
	service.now = func() time.Time { return now }
	principal := Principal{
		UserID: userID, WorkspaceID: workspaceID,
		CredentialType: "session", CredentialID: "30000000-0000-4000-8000-000000000001",
	}

	pairing, err := service.CreatePairingSession(context.Background(), principal)
	if err != nil {
		t.Fatalf("CreatePairingSession() error = %v", err)
	}
	if pairing.ID == "" || !strings.HasPrefix(pairing.Secret, pairingTokenPrefix) {
		t.Fatalf("pairing = %+v", pairing)
	}
	if repository.createPairing.SecretHash == pairing.Secret ||
		repository.createPairing.SecretHash != tokenDigest(pairing.Secret) {
		t.Fatal("pairing repository received plaintext or an incorrect digest")
	}
	if repository.createPairing.WorkspaceID != workspaceID ||
		repository.createPairing.UserID != userID ||
		repository.createPairing.PairingSessionID != pairing.ID ||
		!repository.createPairing.CreatedAt.Equal(now) ||
		!repository.createPairing.ExpiresAt.Equal(now.Add(defaultPairingTTL)) ||
		!pairing.ExpiresAt.Equal(repository.createPairing.ExpiresAt) {
		t.Fatalf("create pairing params/result = %+v / %+v", repository.createPairing, pairing)
	}

	_, err = service.CreatePairingSession(context.Background(), Principal{
		UserID: userID, WorkspaceID: workspaceID, CredentialType: "api_key",
	})
	if !errors.Is(err, ErrUnauthorized) || repository.createPairingCalls != 1 {
		t.Fatalf("API-key CreatePairingSession() = %v, calls = %d", err, repository.createPairingCalls)
	}
}

func TestClaimPairingSessionCreatesNamedSessionWithoutPersistingSecrets(t *testing.T) {
	const (
		pairingID   = "40000000-0000-4000-8000-000000000001"
		workspaceID = "10000000-0000-4000-8000-000000000001"
		userID      = "20000000-0000-4000-8000-000000000001"
	)
	now := time.Date(2026, 7, 18, 4, 0, 0, 0, time.UTC)
	secret := pairingTokenPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	repository := &fakePairingRepository{
		fakeRepository: &fakeRepository{},
		claimedPairing: PairingIdentity{
			SessionID: "30000000-0000-4000-8000-000000000002",
			UserID:    userID, WorkspaceID: workspaceID, Role: "owner", Email: "owner@example.com",
		},
	}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})
	service.now = func() time.Time { return now }

	result, err := service.ClaimPairingSession(context.Background(), pairingID, secret, " Pixel 9 Pro ")
	if err != nil {
		t.Fatalf("ClaimPairingSession() error = %v", err)
	}
	params := repository.claimPairing
	if params.PairingSessionID != pairingID || params.SecretHash != tokenDigest(secret) ||
		params.SecretHash == secret || params.DeviceName != "Pixel 9 Pro" ||
		!params.ClaimedAt.Equal(now) || !params.ExpiresAt.Equal(now.Add(defaultSessionTTL)) ||
		!params.RefreshExpiresAt.Equal(now.Add(defaultRefreshTTL)) {
		t.Fatalf("claim params = %+v", params)
	}
	if params.TokenHash == result.AccessToken || params.TokenHash != tokenDigest(result.AccessToken) ||
		params.RefreshTokenHash == result.RefreshToken ||
		params.RefreshTokenHash != tokenDigest(result.RefreshToken) {
		t.Fatal("pairing claim persisted a plaintext or incorrect session credential")
	}
	if result.User.UserID != userID || result.User.WorkspaceID != workspaceID ||
		result.User.CredentialType != "session" ||
		result.User.CredentialID != repository.claimedPairing.SessionID {
		t.Fatalf("claim result = %+v", result)
	}
}

func TestClaimPairingSessionUsesOneGenericFailure(t *testing.T) {
	repository := &fakePairingRepository{
		fakeRepository: &fakeRepository{}, claimPairingErr: ErrPairingUnavailable,
	}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})
	pairingID := "40000000-0000-4000-8000-000000000001"
	secret := pairingTokenPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	if _, err := service.ClaimPairingSession(context.Background(), pairingID, secret, "Android"); !errors.Is(err, ErrInvalidPairing) {
		t.Fatalf("unavailable ClaimPairingSession() error = %v, want ErrInvalidPairing", err)
	}
	if _, err := service.ClaimPairingSession(context.Background(), pairingID, "short", "Android"); !errors.Is(err, ErrInvalidPairing) || repository.claimPairingCalls != 1 {
		t.Fatalf("malformed ClaimPairingSession() error/calls = %v/%d", err, repository.claimPairingCalls)
	}
}

func TestLoginUsesGenericInvalidCredentialsError(t *testing.T) {
	repository := &fakeRepository{findLoginErr: ErrAccountNotFound}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})

	_, err := service.Login(context.Background(), "missing@example.com", "wrong password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
	if repository.createSessionCalls != 0 {
		t.Fatalf("CreateSession calls = %d, want 0", repository.createSessionCalls)
	}
}

func TestAuthenticateResolvesTokenDigest(t *testing.T) {
	repository := &fakeRepository{principal: Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Role: "owner", Email: "owner@example.com",
	}}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})
	service.now = func() time.Time { return time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC) }

	const token = "va_test_token_with_sufficient_entropy"
	principal, err := service.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	digest := sha256.Sum256([]byte(token))
	if repository.resolveTokenHash != hex.EncodeToString(digest[:]) {
		t.Fatalf("ResolveSession token = %q, want SHA-256 digest", repository.resolveTokenHash)
	}
	if principal.UserID != "user-1" {
		t.Fatalf("principal = %+v", principal)
	}
}

func TestAuthenticateResolvesAPIKeyDigestSeparately(t *testing.T) {
	repository := &fakeAPIKeyRepository{fakeRepository: &fakeRepository{}, apiPrincipal: Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Role: "agent",
		Scopes: []string{ScopeAssetsRead}, CredentialType: "api_key", CredentialID: "key-1",
	}}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})
	now := time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	token := APIKeyTokenPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	principal, err := service.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate(API key) error = %v", err)
	}
	digest := sha256.Sum256([]byte(token))
	if repository.apiTokenHash != hex.EncodeToString(digest[:]) || !repository.apiNow.Equal(now) {
		t.Fatalf("ResolveAPIKey args = %q/%s", repository.apiTokenHash, repository.apiNow)
	}
	if repository.resolveTokenHash != "" || principal.CredentialType != "api_key" {
		t.Fatalf("session resolver/API principal = %q/%+v", repository.resolveTokenHash, principal)
	}
	if err := service.Logout(context.Background(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Logout(API key) error = %v", err)
	}
}

func TestLogoutRevokesTokenDigest(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository, PasswordHasher{Iterations: 1_000})
	now := time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	const token = "va_test_token_with_sufficient_entropy"

	if err := service.Logout(context.Background(), token); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	digest := sha256.Sum256([]byte(token))
	if repository.revokeTokenHash != hex.EncodeToString(digest[:]) || !repository.revokeTime.Equal(now) {
		t.Fatalf("RevokeSession args = (%q, %s)", repository.revokeTokenHash, repository.revokeTime)
	}
}

type fakeRepository struct {
	account              LoginAccount
	findLoginErr         error
	session              NewSession
	createSessionCalls   int
	principal            Principal
	resolveTokenHash     string
	resolveSessionErr    error
	revokeTokenHash      string
	revokeTime           time.Time
	rotate               RotateSessionParams
	rotatedSession       SessionIdentity
	rotateErr            error
	deviceSessions       []DeviceSession
	listWorkspaceID      string
	listUserID           string
	listErr              error
	revokeDevice         RevokeDeviceSessionParams
	revokedDeviceSession DeviceSession
	revokeDeviceErr      error
}

type fakeAPIKeyRepository struct {
	*fakeRepository
	apiPrincipal Principal
	apiTokenHash string
	apiNow       time.Time
	apiErr       error
}

type fakePairingRepository struct {
	*fakeRepository
	createPairing      CreatePairingSessionParams
	createPairingCalls int
	createPairingErr   error
	claimPairing       ClaimPairingSessionParams
	claimPairingCalls  int
	claimedPairing     PairingIdentity
	claimPairingErr    error
}

func (repository *fakePairingRepository) CreatePairingSession(
	_ context.Context,
	params CreatePairingSessionParams,
) error {
	repository.createPairingCalls++
	repository.createPairing = params
	return repository.createPairingErr
}

func (repository *fakePairingRepository) ClaimPairingSession(
	_ context.Context,
	params ClaimPairingSessionParams,
) (PairingIdentity, error) {
	repository.claimPairingCalls++
	repository.claimPairing = params
	return repository.claimedPairing, repository.claimPairingErr
}

func (repository *fakeAPIKeyRepository) ResolveAPIKey(_ context.Context, tokenHash string, now time.Time) (Principal, error) {
	repository.apiTokenHash = tokenHash
	repository.apiNow = now
	return repository.apiPrincipal, repository.apiErr
}

func (f *fakeRepository) FindLoginAccount(context.Context, string) (LoginAccount, error) {
	return f.account, f.findLoginErr
}

func (f *fakeRepository) CreateSession(_ context.Context, session NewSession) error {
	f.createSessionCalls++
	f.session = session
	return nil
}

func (f *fakeRepository) ResolveSession(_ context.Context, tokenHash string, _ time.Time) (Principal, error) {
	f.resolveTokenHash = tokenHash
	return f.principal, f.resolveSessionErr
}

func (f *fakeRepository) RevokeSession(_ context.Context, tokenHash string, now time.Time) error {
	f.revokeTokenHash = tokenHash
	f.revokeTime = now
	return nil
}

func (f *fakeRepository) RotateSession(
	_ context.Context,
	params RotateSessionParams,
) (SessionIdentity, error) {
	f.rotate = params
	return f.rotatedSession, f.rotateErr
}

func (f *fakeRepository) ListDeviceSessions(
	_ context.Context,
	workspaceID,
	userID string,
	now time.Time,
) ([]DeviceSession, error) {
	f.listWorkspaceID = workspaceID
	f.listUserID = userID
	return append([]DeviceSession(nil), f.deviceSessions...), f.listErr
}

func (f *fakeRepository) RevokeDeviceSession(
	_ context.Context,
	params RevokeDeviceSessionParams,
) (DeviceSession, error) {
	f.revokeDevice = params
	return f.revokedDeviceSession, f.revokeDeviceErr
}
