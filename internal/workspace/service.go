// Package workspace manages the authenticated workspace profile.
package workspace

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

var (
	ErrForbidden       = errors.New("forbidden")
	ErrInvalidInput    = errors.New("invalid workspace input")
	ErrNotFound        = errors.New("workspace not found")
	ErrVersionConflict = errors.New("workspace version conflict")
)

type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type UpdateInput struct {
	Name string `json:"name"`
}

type UpdateParams struct {
	WorkspaceID, AuditID, ActorID, RequestID string
	Name                                     string
	ExpectedVersion                          int64
	UpdatedAt                                time.Time
}

type Repository interface {
	Get(context.Context, string) (Workspace, error)
	Update(context.Context, UpdateParams) (Workspace, error)
}

type Service struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader, now: time.Now}
}

func (service *Service) Get(ctx context.Context, principal auth.Principal) (Workspace, error) {
	workspaceID, err := authorizeRead(principal)
	if err != nil {
		return Workspace{}, err
	}
	result, err := service.repository.Get(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Workspace{}, ErrNotFound
		}
		return Workspace{}, fmt.Errorf("get workspace profile: %w", err)
	}
	return result, nil
}

func (service *Service) Update(
	ctx context.Context,
	principal auth.Principal,
	expectedVersion int64,
	input UpdateInput,
	requestID string,
) (Workspace, error) {
	workspaceID, err := authorizeWrite(principal)
	if err != nil {
		return Workspace{}, err
	}
	name, validName := normalizeName(input.Name)
	if expectedVersion < 1 || !validName || !validRequestID(requestID) {
		return Workspace{}, ErrInvalidInput
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Workspace{}, fmt.Errorf("generate workspace audit identifier: %w", err)
	}
	result, err := service.repository.Update(ctx, UpdateParams{
		WorkspaceID: workspaceID, AuditID: auditID, ActorID: principal.UserID,
		RequestID: requestID, Name: name, ExpectedVersion: expectedVersion,
		UpdatedAt: service.now().UTC(),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return Workspace{}, ErrNotFound
		case errors.Is(err, ErrVersionConflict):
			return Workspace{}, ErrVersionConflict
		default:
			return Workspace{}, fmt.Errorf("update workspace profile: %w", err)
		}
	}
	return result, nil
}

func authorizeRead(principal auth.Principal) (string, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return "", ErrForbidden
	}
	workspaceID, valid := identifier.NormalizeUUID(principal.WorkspaceID)
	if !valid {
		return "", ErrForbidden
	}
	return workspaceID, nil
}

func authorizeWrite(principal auth.Principal) (string, error) {
	workspaceID, err := authorizeRead(principal)
	if err != nil || principal.Role != "owner" || !principal.Can(auth.ScopeAdminWrite) {
		return "", ErrForbidden
	}
	if _, valid := identifier.NormalizeUUID(principal.UserID); !valid {
		return "", ErrForbidden
	}
	return workspaceID, nil
}

func normalizeName(value string) (string, bool) {
	value = strings.TrimSpace(value)
	length := utf8.RuneCountInString(value)
	if length < 1 || length > 200 {
		return "", false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", false
		}
	}
	return value, true
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
