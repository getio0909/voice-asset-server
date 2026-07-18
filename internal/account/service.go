// Package account manages authenticated user account settings.
package account

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const (
	minPasswordCharacters = 12
	maxPasswordBytes      = 1024
)

var (
	ErrForbidden          = errors.New("account change is forbidden")
	ErrInvalidInput       = errors.New("invalid account input")
	ErrInvalidCredentials = errors.New("invalid account credentials")
	ErrCredentialsChanged = errors.New("account credentials changed")
	ErrNotFound           = errors.New("account not found")
)

type ChangePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type ChangePasswordParams struct {
	WorkspaceID          string
	UserID               string
	AuditID              string
	RequestID            string
	ExpectedPasswordHash string
	NewPasswordHash      string
	ChangedAt            time.Time
}

type ChangePasswordResult struct {
	RevokedSessions int
}

type Repository interface {
	GetPasswordHash(context.Context, string, string) (string, error)
	ChangePassword(context.Context, ChangePasswordParams) (ChangePasswordResult, error)
}

type Service struct {
	repository Repository
	hasher     auth.PasswordHasher
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository, hasher auth.PasswordHasher) *Service {
	return &Service{repository: repository, hasher: hasher, random: rand.Reader, now: time.Now}
}

func (service *Service) ChangePassword(
	ctx context.Context,
	principal auth.Principal,
	input ChangePasswordInput,
	requestID string,
) (ChangePasswordResult, error) {
	workspaceID, userID, err := authorizeSession(principal)
	if err != nil {
		return ChangePasswordResult{}, err
	}
	if !validCurrentPassword(input.CurrentPassword) || !validNewPassword(input.NewPassword) ||
		input.CurrentPassword == input.NewPassword || !validRequestID(requestID) {
		return ChangePasswordResult{}, ErrInvalidInput
	}

	currentHash, err := service.repository.GetPasswordHash(ctx, workspaceID, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ChangePasswordResult{}, ErrForbidden
		}
		return ChangePasswordResult{}, fmt.Errorf("get account password: %w", err)
	}
	matched, err := service.hasher.Verify(currentHash, input.CurrentPassword)
	if err != nil {
		return ChangePasswordResult{}, fmt.Errorf("verify current password: %w", err)
	}
	if !matched {
		return ChangePasswordResult{}, ErrInvalidCredentials
	}
	newHash, err := service.hasher.Hash(input.NewPassword)
	if err != nil {
		return ChangePasswordResult{}, fmt.Errorf("hash new password: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return ChangePasswordResult{}, fmt.Errorf("generate password-change audit identifier: %w", err)
	}
	result, err := service.repository.ChangePassword(ctx, ChangePasswordParams{
		WorkspaceID: workspaceID, UserID: userID, AuditID: auditID, RequestID: requestID,
		ExpectedPasswordHash: currentHash, NewPasswordHash: newHash, ChangedAt: service.now().UTC(),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrCredentialsChanged):
			return ChangePasswordResult{}, ErrCredentialsChanged
		case errors.Is(err, ErrNotFound):
			return ChangePasswordResult{}, ErrForbidden
		default:
			return ChangePasswordResult{}, fmt.Errorf("change account password: %w", err)
		}
	}
	return result, nil
}

func authorizeSession(principal auth.Principal) (string, string, error) {
	workspaceID, workspaceOK := identifier.NormalizeUUID(principal.WorkspaceID)
	userID, userOK := identifier.NormalizeUUID(principal.UserID)
	_, sessionOK := identifier.NormalizeUUID(principal.CredentialID)
	if !workspaceOK || !userOK || !sessionOK || principal.CredentialType != "session" {
		return "", "", ErrForbidden
	}
	return workspaceID, userID, nil
}

func validCurrentPassword(value string) bool {
	return value != "" && len(value) <= maxPasswordBytes && utf8.ValidString(value)
}

func validNewPassword(value string) bool {
	return len(value) <= maxPasswordBytes && utf8.ValidString(value) &&
		utf8.RuneCountInString(value) >= minPasswordCharacters
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
