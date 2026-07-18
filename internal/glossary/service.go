package glossary

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/llm"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const (
	maxEntries         = 500
	maxAliases         = 50
	maxContextTerms    = 50
	maxEntriesJSONSize = 512 * 1024
)

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`)

type Repository interface {
	Create(context.Context, CreateParams) (Set, error)
	List(context.Context, string) ([]Set, error)
	AddVersion(context.Context, AddVersionParams) (Set, error)
	UpdateState(context.Context, UpdateStateParams) (Set, error)
	Resolve(context.Context, string, string, string) ([]resolvedSet, error)
}

type CreateParams struct {
	SetID, VersionID, AuditID, WorkspaceID, CreatedBy string
	DisplayName, ScopeType, State                     string
	ScopeID                                           *string
	EntriesJSON                                       json.RawMessage
}

type AddVersionParams struct {
	SetID, VersionID, AuditID, WorkspaceID, CreatedBy string
	ExpectedResourceVersion                           int64
	EntriesJSON                                       json.RawMessage
}

type UpdateStateParams struct {
	SetID, AuditID, WorkspaceID, UpdatedBy, State string
	ExpectedResourceVersion                       int64
}

type Service struct {
	repository Repository
	random     io.Reader
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader}
}

func (service *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput) (Set, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Set{}, ErrForbidden
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.ScopeType = strings.TrimSpace(input.ScopeType)
	input.State = strings.TrimSpace(input.State)
	if input.State == "" {
		input.State = StateEnabled
	}
	scopeID, err := normalizeScope(input.ScopeType, input.ScopeID)
	if !validText(input.DisplayName, 100) ||
		(input.State != StateEnabled && input.State != StateDisabled) || err != nil {
		return Set{}, ErrInvalidInput
	}
	entriesJSON, err := normalizeAndEncodeEntries(input.Entries)
	if err != nil {
		return Set{}, ErrInvalidInput
	}
	setID, err := service.newID()
	if err != nil {
		return Set{}, err
	}
	versionID, err := service.newID()
	if err != nil {
		return Set{}, err
	}
	auditID, err := service.newID()
	if err != nil {
		return Set{}, err
	}
	result, err := service.repository.Create(ctx, CreateParams{
		SetID: setID, VersionID: versionID, AuditID: auditID,
		WorkspaceID: principal.WorkspaceID, CreatedBy: principal.UserID,
		DisplayName: input.DisplayName, ScopeType: input.ScopeType,
		ScopeID: scopeID, State: input.State, EntriesJSON: entriesJSON,
	})
	return result, publicRepositoryError("create glossary set", err)
}

func (service *Service) List(ctx context.Context, principal auth.Principal) ([]Set, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return nil, ErrForbidden
	}
	sets, err := service.repository.List(ctx, principal.WorkspaceID)
	if err != nil {
		return nil, publicRepositoryError("list glossary sets", err)
	}
	return sets, nil
}

func (service *Service) AddVersion(ctx context.Context, principal auth.Principal, setID string, expected int64, input AddVersionInput) (Set, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Set{}, ErrForbidden
	}
	setID, ok := identifier.NormalizeUUID(setID)
	if !ok || expected < 1 {
		return Set{}, ErrInvalidInput
	}
	entriesJSON, err := normalizeAndEncodeEntries(input.Entries)
	if err != nil {
		return Set{}, ErrInvalidInput
	}
	versionID, err := service.newID()
	if err != nil {
		return Set{}, err
	}
	auditID, err := service.newID()
	if err != nil {
		return Set{}, err
	}
	result, err := service.repository.AddVersion(ctx, AddVersionParams{
		SetID: setID, VersionID: versionID, AuditID: auditID,
		WorkspaceID: principal.WorkspaceID, CreatedBy: principal.UserID,
		ExpectedResourceVersion: expected, EntriesJSON: entriesJSON,
	})
	return result, publicRepositoryError("add glossary version", err)
}

func (service *Service) Update(ctx context.Context, principal auth.Principal, setID string, expected int64, input UpdateInput) (Set, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Set{}, ErrForbidden
	}
	setID, ok := identifier.NormalizeUUID(setID)
	if !ok || expected < 1 || input.State == nil {
		return Set{}, ErrInvalidInput
	}
	state := strings.TrimSpace(*input.State)
	if state != StateEnabled && state != StateDisabled {
		return Set{}, ErrInvalidInput
	}
	auditID, err := service.newID()
	if err != nil {
		return Set{}, err
	}
	result, err := service.repository.UpdateState(ctx, UpdateStateParams{
		SetID: setID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		UpdatedBy: principal.UserID, State: state, ExpectedResourceVersion: expected,
	})
	return result, publicRepositoryError("update glossary set", err)
}

// ResolveForAsset applies workspace rules first and asset rules last. Rules
// with duplicate aliases are replaced by the more-specific set as a unit.
func (service *Service) ResolveForAsset(ctx context.Context, workspaceID, assetID string) (Resolution, error) {
	return service.ResolveForAssetWithDefault(ctx, workspaceID, assetID, "")
}

func (service *Service) ResolveForAssetWithDefault(ctx context.Context, workspaceID, assetID, defaultSetID string) (Resolution, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(assetID) == "" {
		return Resolution{}, ErrInvalidInput
	}
	if strings.TrimSpace(defaultSetID) != "" {
		var ok bool
		defaultSetID, ok = identifier.NormalizeUUID(defaultSetID)
		if !ok {
			return Resolution{}, ErrInvalidInput
		}
	}
	sets, err := service.repository.Resolve(ctx, workspaceID, assetID, defaultSetID)
	if err != nil {
		return Resolution{}, publicRepositoryError("resolve glossary sets", err)
	}
	sort.SliceStable(sets, func(i, j int) bool {
		if scopeRank(sets[i].ScopeType) != scopeRank(sets[j].ScopeType) {
			return scopeRank(sets[i].ScopeType) < scopeRank(sets[j].ScopeType)
		}
		return sets[i].ID < sets[j].ID
	})
	type snapshotSet struct {
		ID        string  `json:"id"`
		ScopeType string  `json:"scope_type"`
		ScopeID   *string `json:"scope_id,omitempty"`
		Version   int     `json:"version"`
	}
	snapshot := struct {
		Sets []snapshotSet `json:"sets"`
	}{Sets: make([]snapshotSet, 0, len(sets))}
	rules := make([]llm.GlossaryRule, 0)
	positions := make(map[string]int)
	for _, set := range sets {
		snapshot.Sets = append(snapshot.Sets, snapshotSet{
			ID: set.ID, ScopeType: set.ScopeType, ScopeID: clonePointer(set.ScopeID), Version: set.CurrentVersion,
		})
		for _, rule := range set.Entries {
			position := -1
			for _, alias := range rule.Aliases {
				if existing, exists := positions[aliasKey(rule.Language, alias)]; exists {
					position = existing
					break
				}
			}
			if position < 0 {
				position = len(rules)
				rules = append(rules, rule)
			} else {
				rules[position] = rule
			}
			for _, alias := range rule.Aliases {
				positions[aliasKey(rule.Language, alias)] = position
			}
		}
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return Resolution{}, fmt.Errorf("encode glossary snapshot: %w", err)
	}
	return Resolution{Rules: rules, Snapshot: snapshotJSON}, nil
}

func normalizeAndEncodeEntries(inputs []Entry) (json.RawMessage, error) {
	if len(inputs) < 1 || len(inputs) > maxEntries {
		return nil, ErrInvalidInput
	}
	entries := make([]Entry, 0, len(inputs))
	seenAliases := make(map[string]struct{})
	for _, input := range inputs {
		input.CanonicalForm = strings.TrimSpace(input.CanonicalForm)
		input.Language = strings.TrimSpace(input.Language)
		input.Description = strings.TrimSpace(input.Description)
		if !validText(input.CanonicalForm, 200) || len(input.Aliases) < 1 || len(input.Aliases) > maxAliases ||
			(input.Language != "*" && !languagePattern.MatchString(input.Language)) ||
			len(input.ContextTerms) > maxContextTerms || len(input.ForbiddenContexts) > maxContextTerms ||
			input.Priority < 1 || input.Priority > 1000 || !validOptionalText(input.Description, 500) {
			return nil, ErrInvalidInput
		}
		var err error
		input.Aliases, err = normalizeTerms(input.Aliases, 200, input.Regex, seenAliases)
		if err != nil {
			return nil, ErrInvalidInput
		}
		input.ContextTerms, err = normalizeTerms(input.ContextTerms, 200, false, nil)
		if err != nil {
			return nil, ErrInvalidInput
		}
		input.ForbiddenContexts, err = normalizeTerms(input.ForbiddenContexts, 200, false, nil)
		if err != nil {
			return nil, ErrInvalidInput
		}
		entries = append(entries, input)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Priority > entries[j].Priority })
	encoded, err := json.Marshal(entries)
	if err != nil || len(encoded) > maxEntriesJSONSize {
		return nil, ErrInvalidInput
	}
	return encoded, nil
}

func normalizeTerms(values []string, maxRunes int, compileRegex bool, globalSeen map[string]struct{}) ([]string, error) {
	result := make([]string, 0, len(values))
	localSeen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !validText(value, maxRunes) {
			return nil, ErrInvalidInput
		}
		if compileRegex {
			if len(value) > 500 {
				return nil, ErrInvalidInput
			}
			if _, err := regexp.Compile(value); err != nil {
				return nil, ErrInvalidInput
			}
		}
		key := strings.ToLower(value)
		if _, duplicate := localSeen[key]; duplicate {
			return nil, ErrInvalidInput
		}
		if globalSeen != nil {
			if _, duplicate := globalSeen[key]; duplicate {
				return nil, ErrInvalidInput
			}
			globalSeen[key] = struct{}{}
		}
		localSeen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func decodeEntries(raw []byte) ([]Entry, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var entries []Entry
	if err := decoder.Decode(&entries); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("glossary entries contain trailing JSON")
	}
	if len(entries) < 1 || len(entries) > maxEntries {
		return nil, ErrInvalidInput
	}
	return entries, nil
}

func normalizeScope(scopeType string, scopeID *string) (*string, error) {
	switch scopeType {
	case ScopeWorkspace:
		if scopeID != nil && strings.TrimSpace(*scopeID) != "" {
			return nil, ErrInvalidInput
		}
		return nil, nil
	case ScopeCollection, ScopeAsset:
		if scopeID == nil {
			return nil, ErrInvalidInput
		}
		normalized, ok := identifier.NormalizeUUID(*scopeID)
		if !ok {
			return nil, ErrInvalidInput
		}
		return &normalized, nil
	default:
		return nil, ErrInvalidInput
	}
}

func publicRepositoryError(operation string, err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{ErrConflict, ErrNotFound, ErrInvalidInput} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return fmt.Errorf("%w: %s", ErrRepository, operation)
}

func validText(value string, maxRunes int) bool {
	return strings.TrimSpace(value) != "" && validOptionalText(value, maxRunes)
}

func validOptionalText(value string, maxRunes int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, character := range value {
		if character == 0 || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func (service *Service) newID() (string, error) {
	value, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return "", fmt.Errorf("generate glossary identifier: %w", err)
	}
	return value, nil
}

func aliasKey(language, alias string) string {
	return strings.ToLower(language + "\x00" + alias)
}

func scopeRank(scope string) int {
	switch scope {
	case ScopeWorkspace:
		return 1
	case ScopeCollection:
		return 2
	case ScopeAsset:
		return 3
	default:
		return 0
	}
}

func clonePointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
