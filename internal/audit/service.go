// Package audit records immutable security-relevant actions.
package audit

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const maxMetadataBytes = 16 * 1024

var (
	ErrInvalidInput = errors.New("invalid audit input")
	actionPattern   = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,99}$`)
	targetPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,99}$`)
)

type RecordInput struct {
	Principal  auth.Principal
	Action     string
	TargetType string
	TargetID   string
	RequestID  string
	Metadata   map[string]any
}

type Entry struct {
	ID          string
	WorkspaceID string
	ActorID     string
	ActorType   string
	Action      string
	TargetType  string
	TargetID    string
	RequestID   string
	Metadata    json.RawMessage
}

type Repository interface {
	Record(context.Context, Entry) error
}

type Service struct {
	repository Repository
	random     io.Reader
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader}
}

func (service *Service) Record(ctx context.Context, input RecordInput) error {
	workspaceID, workspaceValid := identifier.NormalizeUUID(input.Principal.WorkspaceID)
	actorID, actorValid := identifier.NormalizeUUID(input.Principal.UserID)
	targetID := ""
	if input.TargetID != "" {
		var targetValid bool
		targetID, targetValid = identifier.NormalizeUUID(input.TargetID)
		if !targetValid {
			return ErrInvalidInput
		}
	}
	if !workspaceValid || !actorValid || !actionPattern.MatchString(input.Action) ||
		!targetPattern.MatchString(input.TargetType) || !validRequestID(input.RequestID) {
		return ErrInvalidInput
	}
	metadata := input.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	} else {
		cloned := make(map[string]any, len(metadata)+1)
		for key, value := range metadata {
			cloned[key] = value
		}
		metadata = cloned
	}
	if input.Principal.CredentialType == "api_key" && input.Principal.CredentialID != "" {
		metadata["api_key_id"] = input.Principal.CredentialID
	}
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil || len(encodedMetadata) > maxMetadataBytes {
		return ErrInvalidInput
	}
	entryID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return fmt.Errorf("generate audit identifier: %w", err)
	}
	actorType := "user"
	if input.Principal.Role == "agent" || input.Principal.CredentialType == "api_key" {
		actorType = "agent"
	}
	if err := service.repository.Record(ctx, Entry{
		ID: entryID, WorkspaceID: workspaceID, ActorID: actorID, ActorType: actorType,
		Action: input.Action, TargetType: input.TargetType, TargetID: targetID,
		RequestID: input.RequestID, Metadata: encodedMetadata,
	}); err != nil {
		return fmt.Errorf("record audit entry: %w", err)
	}
	return nil
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
