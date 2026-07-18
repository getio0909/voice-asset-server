package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

var (
	ErrInvalidEmail         = errors.New("invalid email")
	ErrWeakPassword         = errors.New("password must contain between 12 and 1024 characters")
	ErrInvalidWorkspaceName = errors.New("workspace name must contain between 1 and 200 characters")
	ErrOwnerExists          = errors.New("owner already exists")
)

type OwnerInput struct {
	Email         string
	Password      string
	WorkspaceName string
}

type NewOwner struct {
	UserID        string
	WorkspaceID   string
	AuditID       string
	Email         string
	PasswordHash  string
	WorkspaceName string
}

type OwnerCreator interface {
	CreateOwner(ctx context.Context, owner NewOwner) (Principal, error)
}

type BootstrapService struct {
	repository OwnerCreator
	hasher     PasswordHasher
	random     io.Reader
}

func NewBootstrapService(repository OwnerCreator, hasher PasswordHasher) *BootstrapService {
	return &BootstrapService{repository: repository, hasher: hasher, random: rand.Reader}
}

func (s *BootstrapService) CreateOwner(ctx context.Context, input OwnerInput) (Principal, error) {
	email, ok := normalizeEmail(input.Email)
	if !ok {
		return Principal{}, ErrInvalidEmail
	}
	passwordLength := utf8.RuneCountInString(input.Password)
	if passwordLength < 12 || passwordLength > 1024 {
		return Principal{}, ErrWeakPassword
	}
	workspaceName := strings.TrimSpace(input.WorkspaceName)
	workspaceNameLength := utf8.RuneCountInString(workspaceName)
	if workspaceNameLength < 1 || workspaceNameLength > 200 {
		return Principal{}, ErrInvalidWorkspaceName
	}
	passwordHash, err := s.hasher.Hash(input.Password)
	if err != nil {
		return Principal{}, fmt.Errorf("hash owner password: %w", err)
	}
	userID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Principal{}, err
	}
	workspaceID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Principal{}, err
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Principal{}, err
	}
	owner, err := s.repository.CreateOwner(ctx, NewOwner{
		UserID:        userID,
		WorkspaceID:   workspaceID,
		AuditID:       auditID,
		Email:         email,
		PasswordHash:  passwordHash,
		WorkspaceName: workspaceName,
	})
	if err != nil {
		if errors.Is(err, ErrOwnerExists) {
			return Principal{}, ErrOwnerExists
		}
		return Principal{}, fmt.Errorf("create owner: %w", err)
	}
	return owner, nil
}
