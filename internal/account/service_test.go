package account

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

const (
	testWorkspaceID = "10000000-0000-4000-8000-000000000001"
	testUserID      = "20000000-0000-4000-8000-000000000001"
	testSessionID   = "30000000-0000-4000-8000-000000000001"
)

func TestChangePasswordVerifiesAndHashesBeforePersisting(t *testing.T) {
	oldHash, err := (auth.PasswordHasher{
		Iterations: 1_000, Random: bytes.NewReader(bytes.Repeat([]byte{0x11}, 16)),
	}).Hash("current-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	repository := &fakeRepository{
		passwordHash: oldHash,
		changed:      ChangePasswordResult{RevokedSessions: 3},
	}
	service := NewService(repository, auth.PasswordHasher{
		Iterations: 1_000, Random: bytes.NewReader(bytes.Repeat([]byte{0x22}, 16)),
	})
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x33}, 16))
	changedAt := time.Date(2026, 7, 17, 20, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return changedAt }

	result, err := service.ChangePassword(context.Background(), sessionPrincipal(), ChangePasswordInput{
		CurrentPassword: "current-password", NewPassword: "new-password-456",
	}, "request-1")
	if err != nil || result.RevokedSessions != 3 {
		t.Fatalf("ChangePassword() = (%+v, %v)", result, err)
	}
	params := repository.changeParams
	if params.WorkspaceID != testWorkspaceID || params.UserID != testUserID ||
		params.ExpectedPasswordHash != oldHash || params.NewPasswordHash == "new-password-456" ||
		params.NewPasswordHash == oldHash || params.AuditID == "" || params.RequestID != "request-1" ||
		!params.ChangedAt.Equal(changedAt) {
		t.Fatalf("change params = %+v", params)
	}
	matched, verifyErr := (auth.PasswordHasher{}).Verify(params.NewPasswordHash, "new-password-456")
	if verifyErr != nil || !matched {
		t.Fatalf("new password hash verification = %t, %v", matched, verifyErr)
	}
}

func TestChangePasswordRejectsInvalidPrincipalAndInputBeforeRepositoryAccess(t *testing.T) {
	valid := sessionPrincipal()
	apiKey := valid
	apiKey.CredentialType = "api_key"
	badSession := valid
	badSession.CredentialID = "not-a-uuid"
	tests := []struct {
		name      string
		principal auth.Principal
		input     ChangePasswordInput
		requestID string
		want      error
	}{
		{name: "api-key", principal: apiKey, input: validInput(), requestID: "request", want: ErrForbidden},
		{name: "invalid-session", principal: badSession, input: validInput(), requestID: "request", want: ErrForbidden},
		{name: "blank-current", principal: valid, input: ChangePasswordInput{NewPassword: "new-password-456"}, requestID: "request", want: ErrInvalidInput},
		{name: "short-new", principal: valid, input: ChangePasswordInput{CurrentPassword: "current", NewPassword: "short"}, requestID: "request", want: ErrInvalidInput},
		{name: "same", principal: valid, input: ChangePasswordInput{CurrentPassword: "same-password", NewPassword: "same-password"}, requestID: "request", want: ErrInvalidInput},
		{name: "oversized-current", principal: valid, input: ChangePasswordInput{CurrentPassword: strings.Repeat("a", 1025), NewPassword: "new-password-456"}, requestID: "request", want: ErrInvalidInput},
		{name: "oversized-new", principal: valid, input: ChangePasswordInput{CurrentPassword: "current", NewPassword: strings.Repeat("a", 1025)}, requestID: "request", want: ErrInvalidInput},
		{name: "invalid-request", principal: valid, input: validInput(), requestID: " bad ", want: ErrInvalidInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := NewService(repository, auth.PasswordHasher{Iterations: 1_000})
			if _, err := service.ChangePassword(context.Background(), test.principal, test.input, test.requestID); !errors.Is(err, test.want) {
				t.Fatalf("ChangePassword() error = %v, want %v", err, test.want)
			}
			if repository.getCalls != 0 || repository.changeCalls != 0 {
				t.Fatalf("repository calls = get:%d change:%d", repository.getCalls, repository.changeCalls)
			}
		})
	}
}

func TestChangePasswordRejectsWrongOrConcurrentlyChangedCredentials(t *testing.T) {
	oldHash, err := (auth.PasswordHasher{
		Iterations: 1_000, Random: bytes.NewReader(bytes.Repeat([]byte{0x44}, 16)),
	}).Hash("current-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	repository := &fakeRepository{passwordHash: oldHash}
	service := NewService(repository, auth.PasswordHasher{
		Iterations: 1_000, Random: bytes.NewReader(bytes.Repeat([]byte{0x55}, 16)),
	})
	if _, err := service.ChangePassword(context.Background(), sessionPrincipal(), ChangePasswordInput{
		CurrentPassword: "wrong-password", NewPassword: "new-password-456",
	}, "request"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong-current ChangePassword() error = %v", err)
	}
	if repository.changeCalls != 0 {
		t.Fatalf("wrong-current change calls = %d", repository.changeCalls)
	}

	repository.changeErr = ErrCredentialsChanged
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x66}, 16))
	if _, err := service.ChangePassword(context.Background(), sessionPrincipal(), validInput(), "request"); !errors.Is(err, ErrCredentialsChanged) {
		t.Fatalf("concurrent ChangePassword() error = %v", err)
	}
}

func sessionPrincipal() auth.Principal {
	return auth.Principal{
		UserID: testUserID, WorkspaceID: testWorkspaceID,
		CredentialType: "session", CredentialID: testSessionID,
	}
}

func validInput() ChangePasswordInput {
	return ChangePasswordInput{CurrentPassword: "current-password", NewPassword: "new-password-456"}
}

type fakeRepository struct {
	passwordHash string
	getErr       error
	getCalls     int
	changed      ChangePasswordResult
	changeParams ChangePasswordParams
	changeErr    error
	changeCalls  int
}

func (repository *fakeRepository) GetPasswordHash(context.Context, string, string) (string, error) {
	repository.getCalls++
	return repository.passwordHash, repository.getErr
}

func (repository *fakeRepository) ChangePassword(
	_ context.Context,
	params ChangePasswordParams,
) (ChangePasswordResult, error) {
	repository.changeCalls++
	repository.changeParams = params
	return repository.changed, repository.changeErr
}

var _ Repository = (*fakeRepository)(nil)
