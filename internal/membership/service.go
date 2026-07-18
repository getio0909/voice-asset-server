// Package membership manages workspace users and role assignments.
package membership

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const (
	defaultListLimit = 50
	maxListLimit     = 100
	maxCursorLength  = 1024
)

var (
	ErrForbidden       = errors.New("forbidden")
	ErrInvalidInput    = errors.New("invalid membership input")
	ErrNotFound        = errors.New("membership not found")
	ErrConflict        = errors.New("membership conflict")
	ErrVersionConflict = errors.New("membership version conflict")
	ErrLastOwner       = errors.New("workspace must retain an active owner")
)

var validRoles = map[string]struct{}{
	"owner": {}, "admin": {}, "editor": {}, "viewer": {}, "agent": {},
}

var validStatuses = map[string]struct{}{"active": {}, "disabled": {}}

type Member struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	Status      string    `json:"status"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type UpdateInput struct {
	Role   *string `json:"role"`
	Status *string `json:"status"`
}

type ListInput struct {
	Limit  int
	Cursor string
	Role   string
	Status string
}

type List struct {
	Items      []Member `json:"items"`
	NextCursor *string  `json:"next_cursor,omitempty"`
}

type CreateParams struct {
	UserID, AuditID, WorkspaceID, ActorID, RequestID string
	Email, PasswordHash, Role                        string
}

type UpdateParams struct {
	UserID, AuditID, WorkspaceID, ActorID, RequestID string
	ExpectedVersion                                  int64
	Role, Status                                     *string
	UpdatedAt                                        time.Time
}

type ListParams struct {
	WorkspaceID     string
	Role            string
	Status          string
	Limit           int
	BeforeUpdatedAt *time.Time
	BeforeID        string
}

type Repository interface {
	Create(context.Context, CreateParams) (Member, error)
	List(context.Context, ListParams) ([]Member, error)
	Update(context.Context, UpdateParams) (Member, error)
}

type Service struct {
	repository Repository
	hasher     auth.PasswordHasher
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, hasher: auth.PasswordHasher{}, random: rand.Reader, now: time.Now}
}

func (service *Service) Create(
	ctx context.Context,
	principal auth.Principal,
	input CreateInput,
	requestID string,
) (Member, error) {
	workspaceID, err := authorizeWrite(principal)
	if err != nil {
		return Member{}, err
	}
	email, validEmail := normalizeEmail(input.Email)
	role, validRole := normalizeChoice(input.Role, validRoles)
	passwordLength := utf8.RuneCountInString(input.Password)
	if !validEmail || !validRole || passwordLength < 12 || passwordLength > 1024 || !validRequestID(requestID) {
		return Member{}, ErrInvalidInput
	}
	passwordHash, err := service.hasher.Hash(input.Password)
	if err != nil {
		return Member{}, fmt.Errorf("hash member password: %w", err)
	}
	userID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Member{}, fmt.Errorf("generate member identifier: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Member{}, fmt.Errorf("generate member audit identifier: %w", err)
	}
	created, err := service.repository.Create(ctx, CreateParams{
		UserID: userID, AuditID: auditID, WorkspaceID: workspaceID,
		ActorID: principal.UserID, RequestID: requestID,
		Email: email, PasswordHash: passwordHash, Role: role,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return Member{}, ErrConflict
		}
		return Member{}, fmt.Errorf("create workspace member: %w", err)
	}
	return created, nil
}

func (service *Service) List(
	ctx context.Context,
	principal auth.Principal,
	input ListInput,
) (List, error) {
	workspaceID, err := authorizeRead(principal)
	if err != nil {
		return List{}, err
	}
	role, validRole := normalizeOptionalChoice(input.Role, validRoles)
	status, validStatus := normalizeOptionalChoice(input.Status, validStatuses)
	if !validRole || !validStatus {
		return List{}, ErrInvalidInput
	}
	binding := strings.Join([]string{workspaceID, role, status}, "\x00")
	limit, beforeAt, beforeID, err := normalizeListInput(input.Limit, input.Cursor, binding)
	if err != nil {
		return List{}, err
	}
	items, err := service.repository.List(ctx, ListParams{
		WorkspaceID: workspaceID, Role: role, Status: status, Limit: limit + 1,
		BeforeUpdatedAt: beforeAt, BeforeID: beforeID,
	})
	if err != nil {
		return List{}, fmt.Errorf("list workspace members: %w", err)
	}
	result := List{Items: items}
	if len(result.Items) > limit {
		result.Items = append([]Member(nil), result.Items[:limit]...)
		last := result.Items[len(result.Items)-1]
		cursor, encodeErr := encodeCursor(binding, last.UpdatedAt, last.ID)
		if encodeErr != nil {
			return List{}, fmt.Errorf("encode member cursor: %w", encodeErr)
		}
		result.NextCursor = &cursor
	}
	if result.Items == nil {
		result.Items = make([]Member, 0)
	}
	return result, nil
}

func (service *Service) Update(
	ctx context.Context,
	principal auth.Principal,
	userID string,
	expectedVersion int64,
	input UpdateInput,
	requestID string,
) (Member, error) {
	workspaceID, err := authorizeWrite(principal)
	if err != nil {
		return Member{}, err
	}
	userID, validID := identifier.NormalizeUUID(userID)
	if !validID {
		return Member{}, ErrNotFound
	}
	role, validRole := normalizeOptionalPointer(input.Role, validRoles)
	status, validStatus := normalizeOptionalPointer(input.Status, validStatuses)
	if expectedVersion < 1 || !validRole || !validStatus || (role == nil && status == nil) || !validRequestID(requestID) {
		return Member{}, ErrInvalidInput
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Member{}, fmt.Errorf("generate member audit identifier: %w", err)
	}
	updated, err := service.repository.Update(ctx, UpdateParams{
		UserID: userID, AuditID: auditID, WorkspaceID: workspaceID,
		ActorID: principal.UserID, RequestID: requestID, ExpectedVersion: expectedVersion,
		Role: role, Status: status, UpdatedAt: service.now().UTC(),
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return Member{}, ErrNotFound
		case errors.Is(err, ErrVersionConflict):
			return Member{}, ErrVersionConflict
		case errors.Is(err, ErrLastOwner):
			return Member{}, ErrLastOwner
		}
		return Member{}, fmt.Errorf("update workspace member: %w", err)
	}
	return updated, nil
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
	return workspaceID, nil
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

func normalizeChoice(value string, allowed map[string]struct{}) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	_, valid := allowed[value]
	return value, valid
}

func normalizeOptionalChoice(value string, allowed map[string]struct{}) (string, bool) {
	if strings.TrimSpace(value) == "" {
		return "", true
	}
	return normalizeChoice(value, allowed)
}

func normalizeOptionalPointer(value *string, allowed map[string]struct{}) (*string, bool) {
	if value == nil {
		return nil, true
	}
	normalized, valid := normalizeChoice(*value, allowed)
	if !valid {
		return nil, false
	}
	return &normalized, true
}

func validRequestID(value string) bool {
	return value != "" && len(value) <= 200 && strings.TrimSpace(value) == value
}

type listCursor struct {
	Binding string `json:"binding"`
	At      string `json:"at"`
	ID      string `json:"id"`
}

func normalizeListInput(limit int, encoded, binding string) (int, *time.Time, string, error) {
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return 0, nil, "", ErrInvalidInput
	}
	if encoded == "" {
		return limit, nil, "", nil
	}
	if len(encoded) > maxCursorLength || strings.TrimSpace(encoded) != encoded {
		return 0, nil, "", ErrInvalidInput
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return 0, nil, "", ErrInvalidInput
	}
	var cursor listCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Binding != binding {
		return 0, nil, "", ErrInvalidInput
	}
	at, err := time.Parse(time.RFC3339Nano, cursor.At)
	if err != nil {
		return 0, nil, "", ErrInvalidInput
	}
	id, validID := identifier.NormalizeUUID(cursor.ID)
	if !validID {
		return 0, nil, "", ErrInvalidInput
	}
	at = at.UTC()
	return limit, &at, id, nil
}

func encodeCursor(binding string, at time.Time, id string) (string, error) {
	id, validID := identifier.NormalizeUUID(id)
	if !validID || at.IsZero() {
		return "", ErrInvalidInput
	}
	payload, err := json.Marshal(listCursor{Binding: binding, At: at.UTC().Format(time.RFC3339Nano), ID: id})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}
