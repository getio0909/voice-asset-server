package asset

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

var (
	ErrForbidden           = errors.New("forbidden")
	ErrInvalidInput        = errors.New("invalid asset input")
	ErrNotFound            = errors.New("asset not found")
	ErrConflict            = errors.New("asset version conflict")
	ErrPurgeNotEligible    = errors.New("asset is not eligible for permanent deletion")
	ErrIdempotencyConflict = errors.New("idempotency key was used for a different request")
)

var languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)

var assetStatuses = map[string]struct{}{
	"draft": {}, "uploading": {}, "processing": {}, "ready": {}, "failed": {}, "trashed": {},
}

var asrProviderIDs = map[string]struct{}{
	"mock_asr": {}, "aliyun_asr": {}, "tencent_asr": {},
}

const (
	defaultListLimit = 20
	maxListLimit     = 100
	maxCursorLength  = 1024
	maxQueryRunes    = 200
)

type Asset struct {
	ID           string       `json:"id"`
	WorkspaceID  string       `json:"workspace_id"`
	CollectionID *string      `json:"collection_id"`
	Title        string       `json:"title"`
	Language     string       `json:"language"`
	Status       string       `json:"status"`
	DurationMS   *int64       `json:"duration_ms"`
	Version      int64        `json:"version"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	Search       *SearchMatch `json:"search,omitempty"`
}

type SearchMatch struct {
	Title       bool         `json:"title"`
	ProviderIDs []string     `json:"provider_ids"`
	Segments    []SegmentHit `json:"segments"`
}

type SegmentHit struct {
	TranscriptID string  `json:"transcript_id"`
	RevisionID   string  `json:"revision_id"`
	SegmentID    string  `json:"segment_id"`
	Ordinal      int     `json:"ordinal"`
	StartMS      int64   `json:"start_ms"`
	EndMS        int64   `json:"end_ms"`
	Speaker      *string `json:"speaker"`
	Text         string  `json:"text"`
}

type CreateInput struct {
	Title    string `json:"title"`
	Language string `json:"language"`
}

type UpdateMetadataInput struct {
	Title        string  `json:"title"`
	Language     string  `json:"language"`
	CollectionID *string `json:"collection_id"`
}

// PurgeInput requires the caller to repeat the canonical asset identifier.
// This makes permanent deletion a deliberate second action after trashing.
type PurgeInput struct {
	Confirmation string `json:"confirmation"`
}

// PurgeRequest is the durable background deletion accepted by the API.
type PurgeRequest struct {
	JobID        string    `json:"job_id"`
	AssetID      string    `json:"asset_id"`
	AssetVersion int64     `json:"asset_version"`
	State        string    `json:"state"`
	RequestedAt  time.Time `json:"requested_at"`
}

type ListInput struct {
	Query         string
	CollectionID  string
	TagID         string
	Status        string
	ProviderID    string
	Speaker       string
	CreatedFrom   string
	CreatedBefore string
	Limit         int
	Cursor        string
}

type ListResult struct {
	Items      []Asset `json:"items"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

type CreateParams struct {
	AssetID        string
	AuditID        string
	WorkspaceID    string
	CreatedBy      string
	Title          string
	Language       string
	IdempotencyKey string
	RequestHash    string
}

type ListParams struct {
	WorkspaceID     string
	Query           string
	CollectionID    string
	TagID           string
	Status          string
	ProviderID      string
	Speaker         string
	CreatedFrom     *time.Time
	CreatedBefore   *time.Time
	Limit           int
	BeforeCreatedAt *time.Time
	BeforeID        string
}

type LifecycleParams struct {
	AuditID         string
	WorkspaceID     string
	AssetID         string
	ActorID         string
	ActorType       string
	CredentialID    string
	RequestID       string
	ExpectedVersion int64
}

type PurgeParams struct {
	JobID, AuditID, WorkspaceID, AssetID, ActorID string
	ActorType, CredentialID, RequestID            string
	IdempotencyKey, RequestHash                   string
	ExpectedVersion                               int64
}

type UpdateMetadataParams struct {
	AuditID         string
	WorkspaceID     string
	AssetID         string
	ActorID         string
	ActorType       string
	CredentialID    string
	RequestID       string
	Title           string
	Language        string
	CollectionID    *string
	ExpectedVersion int64
}

type Repository interface {
	Create(ctx context.Context, params CreateParams) (Asset, bool, error)
	Get(ctx context.Context, workspaceID, assetID string) (Asset, error)
	List(ctx context.Context, params ListParams) ([]Asset, error)
	UpdateMetadata(ctx context.Context, params UpdateMetadataParams) (Asset, error)
	Trash(ctx context.Context, params LifecycleParams) (Asset, error)
	Restore(ctx context.Context, params LifecycleParams) (Asset, error)
	RequestPurge(ctx context.Context, params PurgeParams) (PurgeRequest, bool, error)
	GetPurge(ctx context.Context, workspaceID, jobID string) (PurgeRequest, error)
}

type Service struct {
	repository Repository
	random     io.Reader
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader}
}

func (s *Service) Create(
	ctx context.Context,
	principal auth.Principal,
	input CreateInput,
	idempotencyKey string,
) (Asset, bool, error) {
	if !principal.Can(auth.ScopeAssetsWrite) {
		return Asset{}, false, ErrForbidden
	}
	title := strings.TrimSpace(input.Title)
	language, validLanguage := normalizeLanguage(input.Language)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !validTitle(title) || !validLanguage || !validIdempotencyKey(idempotencyKey) {
		return Asset{}, false, ErrInvalidInput
	}
	assetID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Asset{}, false, err
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Asset{}, false, err
	}
	digest := sha256.Sum256([]byte(title + "\x00" + language))
	created, replayed, err := s.repository.Create(ctx, CreateParams{
		AssetID:        assetID,
		AuditID:        auditID,
		WorkspaceID:    principal.WorkspaceID,
		CreatedBy:      principal.UserID,
		Title:          title,
		Language:       language,
		IdempotencyKey: idempotencyKey,
		RequestHash:    hex.EncodeToString(digest[:]),
	})
	if err != nil {
		if errors.Is(err, ErrIdempotencyConflict) {
			return Asset{}, false, ErrIdempotencyConflict
		}
		return Asset{}, false, fmt.Errorf("create asset: %w", err)
	}
	return created, replayed, nil
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, assetID string) (Asset, error) {
	if !principal.Can(auth.ScopeAssetsRead) {
		return Asset{}, ErrForbidden
	}
	if strings.TrimSpace(assetID) == "" {
		return Asset{}, ErrNotFound
	}
	result, err := s.repository.Get(ctx, principal.WorkspaceID, assetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Asset{}, ErrNotFound
		}
		return Asset{}, fmt.Errorf("get asset: %w", err)
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, principal auth.Principal, input ListInput) (ListResult, error) {
	if !principal.Can(auth.ScopeAssetsRead) {
		return ListResult{}, ErrForbidden
	}
	filter, err := normalizeListFilter(input)
	if err != nil {
		return ListResult{}, ErrInvalidInput
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return ListResult{}, ErrInvalidInput
	}

	beforeCreatedAt, beforeID, err := decodeListCursor(input.Cursor, filter)
	if err != nil {
		return ListResult{}, ErrInvalidInput
	}
	items, err := s.repository.List(ctx, ListParams{
		WorkspaceID:     principal.WorkspaceID,
		Query:           filter.Query,
		CollectionID:    filter.CollectionID,
		TagID:           filter.TagID,
		Status:          filter.Status,
		ProviderID:      filter.ProviderID,
		Speaker:         filter.Speaker,
		CreatedFrom:     filter.CreatedFrom,
		CreatedBefore:   filter.CreatedBefore,
		Limit:           limit + 1,
		BeforeCreatedAt: beforeCreatedAt,
		BeforeID:        beforeID,
	})
	if err != nil {
		return ListResult{}, fmt.Errorf("list assets: %w", err)
	}

	result := ListResult{Items: items}
	if len(items) > limit {
		result.Items = append([]Asset(nil), items[:limit]...)
		cursor, encodeErr := encodeListCursor(result.Items[len(result.Items)-1], filter)
		if encodeErr != nil {
			return ListResult{}, fmt.Errorf("encode asset cursor: %w", encodeErr)
		}
		result.NextCursor = &cursor
	}
	return result, nil
}

func (s *Service) Trash(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	expectedVersion int64,
	requestID string,
) (Asset, error) {
	return s.changeLifecycle(ctx, principal, assetID, expectedVersion, requestID, true)
}

func (s *Service) Restore(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	expectedVersion int64,
	requestID string,
) (Asset, error) {
	return s.changeLifecycle(ctx, principal, assetID, expectedVersion, requestID, false)
}

// RequestPurge schedules irreversible deletion. Only an Owner may request it;
// the repository additionally requires an exact trashed version and no active
// jobs before moving the asset into its internal purging state.
func (s *Service) RequestPurge(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	expectedVersion int64,
	input PurgeInput,
	idempotencyKey,
	requestID string,
) (PurgeRequest, bool, error) {
	if principal.Role != "owner" || !principal.Can(auth.ScopeAssetsWrite) {
		return PurgeRequest{}, false, ErrForbidden
	}
	assetID, validAssetID := identifier.NormalizeUUID(assetID)
	_, validConfirmation := identifier.NormalizeUUID(input.Confirmation)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !validAssetID || !validConfirmation || input.Confirmation != assetID || expectedVersion < 1 ||
		!validIdempotencyKey(idempotencyKey) || !validRequestID(requestID) {
		return PurgeRequest{}, false, ErrInvalidInput
	}
	jobID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return PurgeRequest{}, false, fmt.Errorf("generate asset purge job identifier: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return PurgeRequest{}, false, fmt.Errorf("generate asset purge audit identifier: %w", err)
	}
	digest := sha256.Sum256([]byte(assetID + "\x00" + strconv.FormatInt(expectedVersion, 10)))
	requested, replayed, err := s.repository.RequestPurge(ctx, PurgeParams{
		JobID: jobID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		AssetID: assetID, ActorID: principal.UserID, ActorType: assetActorType(principal),
		CredentialID: principal.CredentialID, RequestID: requestID,
		IdempotencyKey: idempotencyKey, RequestHash: hex.EncodeToString(digest[:]),
		ExpectedVersion: expectedVersion,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return PurgeRequest{}, false, ErrNotFound
		case errors.Is(err, ErrConflict):
			return PurgeRequest{}, false, ErrConflict
		case errors.Is(err, ErrPurgeNotEligible):
			return PurgeRequest{}, false, ErrPurgeNotEligible
		case errors.Is(err, ErrIdempotencyConflict):
			return PurgeRequest{}, false, ErrIdempotencyConflict
		default:
			return PurgeRequest{}, false, fmt.Errorf("request asset purge: %w", err)
		}
	}
	return requested, replayed, nil
}

func (s *Service) GetPurge(
	ctx context.Context,
	principal auth.Principal,
	jobID string,
) (PurgeRequest, error) {
	if principal.Role != "owner" || !principal.Can(auth.ScopeAssetsWrite) {
		return PurgeRequest{}, ErrForbidden
	}
	jobID, validJobID := identifier.NormalizeUUID(jobID)
	if !validJobID {
		return PurgeRequest{}, ErrNotFound
	}
	result, err := s.repository.GetPurge(ctx, principal.WorkspaceID, jobID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return PurgeRequest{}, ErrNotFound
		}
		return PurgeRequest{}, fmt.Errorf("get asset purge: %w", err)
	}
	return result, nil
}

func (s *Service) changeLifecycle(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	expectedVersion int64,
	requestID string,
	trash bool,
) (Asset, error) {
	if !principal.Can(auth.ScopeAssetsWrite) {
		return Asset{}, ErrForbidden
	}
	assetID, validAssetID := identifier.NormalizeUUID(assetID)
	if !validAssetID || expectedVersion < 1 || !validRequestID(requestID) {
		return Asset{}, ErrInvalidInput
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Asset{}, fmt.Errorf("generate asset lifecycle audit identifier: %w", err)
	}
	params := LifecycleParams{
		AuditID: auditID, WorkspaceID: principal.WorkspaceID, AssetID: assetID,
		ActorID: principal.UserID, ActorType: assetActorType(principal),
		CredentialID: principal.CredentialID, RequestID: requestID,
		ExpectedVersion: expectedVersion,
	}
	var updated Asset
	if trash {
		updated, err = s.repository.Trash(ctx, params)
	} else {
		updated, err = s.repository.Restore(ctx, params)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return Asset{}, ErrNotFound
		case errors.Is(err, ErrConflict):
			return Asset{}, ErrConflict
		default:
			return Asset{}, fmt.Errorf("change asset lifecycle: %w", err)
		}
	}
	return updated, nil
}

func (s *Service) UpdateMetadata(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	expectedVersion int64,
	input UpdateMetadataInput,
	requestID string,
) (Asset, error) {
	if !principal.Can(auth.ScopeMetadataWrite) {
		return Asset{}, ErrForbidden
	}
	assetID, validAssetID := identifier.NormalizeUUID(assetID)
	title := strings.TrimSpace(input.Title)
	language, validLanguage := normalizeLanguage(input.Language)
	var collectionID *string
	if input.CollectionID != nil {
		canonicalID, validCollectionID := identifier.NormalizeUUID(*input.CollectionID)
		if !validCollectionID {
			return Asset{}, ErrInvalidInput
		}
		collectionID = &canonicalID
	}
	if !validAssetID || expectedVersion < 1 || !validTitle(title) || !validLanguage || !validRequestID(requestID) {
		return Asset{}, ErrInvalidInput
	}
	auditID, err := identifier.NewUUIDFrom(s.random)
	if err != nil {
		return Asset{}, fmt.Errorf("generate asset metadata audit identifier: %w", err)
	}
	updated, err := s.repository.UpdateMetadata(ctx, UpdateMetadataParams{
		AuditID: auditID, WorkspaceID: principal.WorkspaceID, AssetID: assetID,
		ActorID: principal.UserID, ActorType: assetActorType(principal),
		CredentialID: principal.CredentialID, RequestID: requestID,
		Title: title, Language: language, CollectionID: collectionID,
		ExpectedVersion: expectedVersion,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return Asset{}, ErrNotFound
		case errors.Is(err, ErrConflict):
			return Asset{}, ErrConflict
		default:
			return Asset{}, fmt.Errorf("update asset metadata: %w", err)
		}
	}
	return updated, nil
}

type listCursor struct {
	CreatedAt  string `json:"created_at"`
	ID         string `json:"id"`
	FilterHash string `json:"filter_hash"`
}

type normalizedListFilter struct {
	Query         string
	CollectionID  string
	TagID         string
	Status        string
	ProviderID    string
	Speaker       string
	CreatedFrom   *time.Time
	CreatedBefore *time.Time
}

func encodeListCursor(item Asset, filter normalizedListFilter) (string, error) {
	payload, err := json.Marshal(listCursor{
		CreatedAt:  item.CreatedAt.UTC().Format(time.RFC3339Nano),
		ID:         item.ID,
		FilterHash: listFilterHash(filter),
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeListCursor(value string, filter normalizedListFilter) (*time.Time, string, error) {
	if value == "" {
		return nil, "", nil
	}
	if len(value) > maxCursorLength || strings.TrimSpace(value) != value {
		return nil, "", ErrInvalidInput
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, "", ErrInvalidInput
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var cursor listCursor
	if err := decoder.Decode(&cursor); err != nil {
		return nil, "", ErrInvalidInput
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, "", ErrInvalidInput
	}
	createdAt, err := time.Parse(time.RFC3339Nano, cursor.CreatedAt)
	if err != nil {
		return nil, "", ErrInvalidInput
	}
	canonicalID, validID := identifier.NormalizeUUID(cursor.ID)
	if !validID || cursor.FilterHash != listFilterHash(filter) {
		return nil, "", ErrInvalidInput
	}
	createdAt = createdAt.UTC()
	return &createdAt, canonicalID, nil
}

func listFilterHash(filter normalizedListFilter) string {
	createdFrom := ""
	if filter.CreatedFrom != nil {
		createdFrom = filter.CreatedFrom.UTC().Format(time.RFC3339Nano)
	}
	createdBefore := ""
	if filter.CreatedBefore != nil {
		createdBefore = filter.CreatedBefore.UTC().Format(time.RFC3339Nano)
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		filter.Query, filter.CollectionID, filter.TagID, filter.Status,
		filter.ProviderID, filter.Speaker, createdFrom, createdBefore,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func normalizeListFilter(input ListInput) (normalizedListFilter, error) {
	filter := normalizedListFilter{Query: strings.TrimSpace(input.Query)}
	if !validSearchQuery(filter.Query) {
		return normalizedListFilter{}, ErrInvalidInput
	}
	if value := strings.TrimSpace(input.CollectionID); value != "" {
		canonical, valid := identifier.NormalizeUUID(value)
		if !valid {
			return normalizedListFilter{}, ErrInvalidInput
		}
		filter.CollectionID = canonical
	}
	if value := strings.TrimSpace(input.TagID); value != "" {
		canonical, valid := identifier.NormalizeUUID(value)
		if !valid {
			return normalizedListFilter{}, ErrInvalidInput
		}
		filter.TagID = canonical
	}
	filter.Status = strings.TrimSpace(input.Status)
	if filter.Status != "" {
		if _, valid := assetStatuses[filter.Status]; !valid {
			return normalizedListFilter{}, ErrInvalidInput
		}
	}
	filter.ProviderID = strings.TrimSpace(input.ProviderID)
	if filter.ProviderID != "" {
		if _, valid := asrProviderIDs[filter.ProviderID]; !valid {
			return normalizedListFilter{}, ErrInvalidInput
		}
	}
	filter.Speaker = strings.TrimSpace(input.Speaker)
	if !validSearchQuery(filter.Speaker) {
		return normalizedListFilter{}, ErrInvalidInput
	}
	var err error
	filter.CreatedFrom, err = parseListTime(input.CreatedFrom)
	if err != nil {
		return normalizedListFilter{}, ErrInvalidInput
	}
	filter.CreatedBefore, err = parseListTime(input.CreatedBefore)
	if err != nil {
		return normalizedListFilter{}, ErrInvalidInput
	}
	if filter.CreatedFrom != nil && filter.CreatedBefore != nil && !filter.CreatedFrom.Before(*filter.CreatedBefore) {
		return normalizedListFilter{}, ErrInvalidInput
	}
	return filter, nil
}

func parseListTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func validTitle(title string) bool {
	length := utf8.RuneCountInString(title)
	if length < 1 || length > 500 {
		return false
	}
	for _, character := range title {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validSearchQuery(query string) bool {
	if utf8.RuneCountInString(query) > maxQueryRunes {
		return false
	}
	for _, character := range query {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validIdempotencyKey(key string) bool {
	if len(key) < 1 || len(key) > 200 {
		return false
	}
	for _, character := range key {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
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

func assetActorType(principal auth.Principal) string {
	if principal.Role == "agent" || principal.CredentialType == "api_key" {
		return "agent"
	}
	return "user"
}

func normalizeLanguage(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "und" {
		return value, true
	}
	if !languagePattern.MatchString(value) {
		return "", false
	}
	parts := strings.Split(value, "-")
	parts[0] = strings.ToLower(parts[0])
	for index := 1; index < len(parts); index++ {
		if len(parts[index]) == 2 {
			parts[index] = strings.ToUpper(parts[index])
		} else {
			parts[index] = strings.ToLower(parts[index])
		}
	}
	return strings.Join(parts, "-"), true
}
