package hotword

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

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const (
	maxEntries            = 500
	maxExpandedTerms      = 500
	maxAliasesPerEntry    = 20
	maxProviderMappingLen = 4 * 1024
	maxEntriesJSONLen     = 128 * 1024
)

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2,8}(?:-[A-Za-z0-9]{1,8})*$`)

type Repository interface {
	Create(ctx context.Context, params CreateParams) (Set, error)
	List(ctx context.Context, workspaceID string) ([]Set, error)
	AddVersion(ctx context.Context, params AddVersionParams) (Set, error)
	UpdateState(ctx context.Context, params UpdateStateParams) (Set, error)
	Resolve(ctx context.Context, workspaceID, assetID string) ([]resolvedSet, error)
}

type CreateParams struct {
	SetID       string
	VersionID   string
	AuditID     string
	WorkspaceID string
	CreatedBy   string
	DisplayName string
	ScopeType   string
	ScopeID     *string
	State       string
	EntriesJSON json.RawMessage
}

type AddVersionParams struct {
	SetID                   string
	VersionID               string
	AuditID                 string
	WorkspaceID             string
	CreatedBy               string
	ExpectedResourceVersion int64
	EntriesJSON             json.RawMessage
}

type UpdateStateParams struct {
	SetID                   string
	AuditID                 string
	WorkspaceID             string
	UpdatedBy               string
	State                   string
	ExpectedResourceVersion int64
}

type Service struct {
	repository Repository
	random     io.Reader
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader}
}

func (service *Service) Create(
	ctx context.Context,
	principal auth.Principal,
	input CreateInput,
) (Set, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Set{}, ErrForbidden
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.ScopeType = strings.TrimSpace(input.ScopeType)
	input.State = strings.TrimSpace(input.State)
	if input.State == "" {
		input.State = StateEnabled
	}
	if !validText(input.DisplayName, 100) ||
		(input.State != StateEnabled && input.State != StateDisabled) {
		return Set{}, ErrInvalidInput
	}
	scopeID, err := normalizeScope(input.ScopeType, input.ScopeID)
	if err != nil {
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
	if err != nil {
		return Set{}, publicRepositoryError("create hotword set", err)
	}
	return result, nil
}

func (service *Service) List(ctx context.Context, principal auth.Principal) ([]Set, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return nil, ErrForbidden
	}
	sets, err := service.repository.List(ctx, principal.WorkspaceID)
	if err != nil {
		return nil, publicRepositoryError("list hotword sets", err)
	}
	return sets, nil
}

func (service *Service) AddVersion(
	ctx context.Context,
	principal auth.Principal,
	setID string,
	expectedResourceVersion int64,
	input AddVersionInput,
) (Set, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Set{}, ErrForbidden
	}
	normalizedSetID, ok := identifier.NormalizeUUID(setID)
	if !ok || expectedResourceVersion < 1 {
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
		SetID: normalizedSetID, VersionID: versionID, AuditID: auditID,
		WorkspaceID: principal.WorkspaceID, CreatedBy: principal.UserID,
		ExpectedResourceVersion: expectedResourceVersion, EntriesJSON: entriesJSON,
	})
	if err != nil {
		return Set{}, publicRepositoryError("add hotword set version", err)
	}
	return result, nil
}

func (service *Service) Update(
	ctx context.Context,
	principal auth.Principal,
	setID string,
	expectedResourceVersion int64,
	input UpdateInput,
) (Set, error) {
	if !principal.Can(auth.ScopeAdminWrite) {
		return Set{}, ErrForbidden
	}
	normalizedSetID, ok := identifier.NormalizeUUID(setID)
	if !ok || expectedResourceVersion < 1 || input.State == nil {
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
		SetID: normalizedSetID, AuditID: auditID,
		WorkspaceID: principal.WorkspaceID, UpdatedBy: principal.UserID,
		State: state, ExpectedResourceVersion: expectedResourceVersion,
	})
	if err != nil {
		return Set{}, publicRepositoryError("update hotword set", err)
	}
	return result, nil
}

// ResolveForAsset compiles all enabled workspace and asset sets into the
// provider-neutral input and returns a provenance snapshot for the immutable
// transcript revision. Collection sets are retained by the data model and will
// participate once collection membership is implemented.
func (service *Service) ResolveForAsset(
	ctx context.Context,
	workspaceID,
	assetID string,
) (Resolution, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(assetID) == "" {
		return Resolution{}, ErrInvalidInput
	}
	sets, err := service.repository.Resolve(ctx, workspaceID, assetID)
	if err != nil {
		return Resolution{}, publicRepositoryError("resolve hotword sets", err)
	}
	sortSetsByPrecedence(sets)
	type snapshotSet struct {
		ID        string  `json:"id"`
		Version   int     `json:"version"`
		ScopeType string  `json:"scope_type"`
		ScopeID   *string `json:"scope_id,omitempty"`
	}
	snapshot := struct {
		Sets []snapshotSet `json:"sets"`
	}{Sets: make([]snapshotSet, 0, len(sets))}
	hotwords := make([]asr.Hotword, 0)
	positions := make(map[string]int)
	for _, set := range sets {
		snapshot.Sets = append(snapshot.Sets, snapshotSet{
			ID: set.ID, Version: set.CurrentVersion,
			ScopeType: set.ScopeType, ScopeID: cloneStringPointer(set.ScopeID),
		})
		for _, entry := range set.Entries {
			if !entry.Enabled {
				continue
			}
			terms := append([]string{entry.Term}, entry.Aliases...)
			for _, term := range terms {
				key := strings.ToLower(term)
				candidate := asr.Hotword{Term: term, Weight: entry.Weight}
				if position, exists := positions[key]; exists {
					hotwords[position] = candidate
					continue
				}
				if len(hotwords) >= maxExpandedTerms {
					return Resolution{}, ErrInvalidInput
				}
				positions[key] = len(hotwords)
				hotwords = append(hotwords, candidate)
			}
		}
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return Resolution{}, fmt.Errorf("encode hotword snapshot: %w", err)
	}
	return Resolution{Hotwords: hotwords, Snapshot: snapshotJSON}, nil
}

func normalizeAndEncodeEntries(inputs []EntryInput) (json.RawMessage, error) {
	if len(inputs) < 1 || len(inputs) > maxEntries {
		return nil, ErrInvalidInput
	}
	entries := make([]Entry, 0, len(inputs))
	seenTerms := make(map[string]struct{})
	expandedTerms := 0
	for _, input := range inputs {
		term := strings.TrimSpace(input.Term)
		language := strings.TrimSpace(input.Language)
		description := strings.TrimSpace(input.Description)
		if !validTerm(term) || !languagePattern.MatchString(language) ||
			input.Weight < 1 || input.Weight > 100 || !validOptionalText(description, 500) ||
			len(input.Aliases) > maxAliasesPerEntry {
			return nil, ErrInvalidInput
		}
		aliases := make([]string, 0, len(input.Aliases))
		allTerms := append([]string{term}, input.Aliases...)
		for index, value := range allTerms {
			value = strings.TrimSpace(value)
			if !validTerm(value) {
				return nil, ErrInvalidInput
			}
			key := strings.ToLower(value)
			if _, duplicate := seenTerms[key]; duplicate {
				return nil, ErrInvalidInput
			}
			seenTerms[key] = struct{}{}
			if index > 0 {
				aliases = append(aliases, value)
			}
			expandedTerms++
			if expandedTerms > maxExpandedTerms {
				return nil, ErrInvalidInput
			}
		}
		mapping, err := normalizeProviderMapping(input.ProviderMapping)
		if err != nil {
			return nil, ErrInvalidInput
		}
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		entries = append(entries, Entry{
			Term: term, Aliases: aliases, Language: language, Weight: input.Weight,
			ProviderMapping: mapping, Enabled: enabled, Description: description,
		})
	}
	encoded, err := json.Marshal(entries)
	if err != nil || len(encoded) > maxEntriesJSONLen {
		return nil, ErrInvalidInput
	}
	return encoded, nil
}

func decodeEntries(value []byte) ([]Entry, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var entries []Entry
	if err := decoder.Decode(&entries); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("hotword entries contain trailing JSON")
	}
	if len(entries) < 1 || len(entries) > maxEntries {
		return nil, ErrInvalidInput
	}
	return entries, nil
}

func normalizeProviderMapping(input map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if len(input) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	if len(input) > 3 {
		return nil, ErrInvalidInput
	}
	result := make(map[string]json.RawMessage, len(input))
	for providerID, value := range input {
		if providerID != asr.MockProviderID && providerID != asr.AliyunProviderID &&
			providerID != asr.TencentProviderID {
			return nil, ErrInvalidInput
		}
		trimmed := bytes.TrimSpace(value)
		if len(trimmed) < 2 || len(trimmed) > maxProviderMappingLen ||
			trimmed[0] != '{' || !json.Valid(trimmed) {
			return nil, ErrInvalidInput
		}
		result[providerID] = append(json.RawMessage(nil), trimmed...)
	}
	return result, nil
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

func validTerm(value string) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < 1 ||
		utf8.RuneCountInString(value) > 30 || strings.ContainsAny(value, ",|\r\n") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validText(value string, maxRunes int) bool {
	return value != "" && validOptionalText(value, maxRunes)
}

func validOptionalText(value string, maxRunes int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func publicRepositoryError(operation string, err error) error {
	if errors.Is(err, ErrConflict) {
		return ErrConflict
	}
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, ErrInvalidInput) {
		return ErrInvalidInput
	}
	return fmt.Errorf("%w: %s", ErrRepository, operation)
}

func (service *Service) newID() (string, error) {
	value, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return "", fmt.Errorf("generate hotword identifier: %w", err)
	}
	return value, nil
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func sortSetsByPrecedence(sets []resolvedSet) {
	sort.SliceStable(sets, func(left, right int) bool {
		leftRank := scopeRank(sets[left].ScopeType)
		rightRank := scopeRank(sets[right].ScopeType)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return sets[left].ID < sets[right].ID
	})
}

func scopeRank(scopeType string) int {
	switch scopeType {
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
