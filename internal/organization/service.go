// Package organization owns workspace collections, tags, annotations, and
// asset processing read models.
package organization

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
	"slices"
	"strings"
	"time"
	"unicode"
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
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid organization input")
	ErrNotFound     = errors.New("organization resource not found")
)

type Collection struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     int64     `json:"version"`
	AssetCount  int64     `json:"asset_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Tag struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Color       *string   `json:"color"`
	AssetCount  int64     `json:"asset_count"`
	CreatedAt   time.Time `json:"created_at"`
}

type Annotation struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	AssetID     string    `json:"asset_id"`
	Kind        string    `json:"kind"`
	StartMS     int64     `json:"start_ms"`
	EndMS       *int64    `json:"end_ms"`
	Body        string    `json:"body"`
	Version     int64     `json:"version"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ProcessingJob struct {
	ID               string    `json:"id"`
	Kind             string    `json:"kind"`
	State            string    `json:"state"`
	Attempts         int       `json:"attempts"`
	MaxAttempts      int       `json:"max_attempts"`
	LastErrorCode    *string   `json:"last_error_code"`
	ResultRevisionID *string   `json:"result_revision_id"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ProcessingStatus struct {
	AssetID     string          `json:"asset_id"`
	AssetStatus string          `json:"asset_status"`
	Active      bool            `json:"active"`
	Jobs        []ProcessingJob `json:"jobs"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type ListInput struct {
	Limit  int
	Cursor string
}

type AnnotationListInput struct {
	AssetID string
	Limit   int
	Cursor  string
}

type AssetTagListInput struct {
	AssetID string
	Limit   int
	Cursor  string
}

type CollectionList struct {
	Items      []Collection `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type TagList struct {
	Items      []Tag   `json:"items"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

type AnnotationList struct {
	Items      []Annotation `json:"items"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type TagMutationInput struct {
	TagIDs []string `json:"tag_ids"`
}

type TagMutationResult struct {
	AssetID      string   `json:"asset_id"`
	TagIDs       []string `json:"tag_ids"`
	ChangedCount int      `json:"changed_count"`
}

type AnnotationCreateInput struct {
	Kind    string `json:"kind"`
	StartMS int64  `json:"start_ms"`
	EndMS   *int64 `json:"end_ms"`
	Body    string `json:"body"`
}

type TagMutationParams struct {
	AuditID, WorkspaceID, AssetID, ActorID, ActorType, CredentialID, RequestID string
	TagIDs                                                                     []string
}

type AnnotationCreateParams struct {
	ID, AuditID, WorkspaceID, AssetID, ActorID, ActorType, CredentialID, RequestID string
	Kind, Body                                                                     string
	StartMS                                                                        int64
	EndMS                                                                          *int64
}

type ListParams struct {
	WorkspaceID     string
	Limit           int
	BeforeCreatedAt *time.Time
	BeforeID        string
}

type AnnotationListParams struct {
	WorkspaceID     string
	AssetID         string
	Limit           int
	BeforeCreatedAt *time.Time
	BeforeID        string
}

type AssetTagListParams struct {
	WorkspaceID     string
	AssetID         string
	Limit           int
	BeforeCreatedAt *time.Time
	BeforeID        string
}

type Repository interface {
	GetCollection(context.Context, string, string) (Collection, error)
	ListCollections(context.Context, ListParams) ([]Collection, error)
	ListTags(context.Context, ListParams) ([]Tag, error)
	ListAssetTags(context.Context, AssetTagListParams) ([]Tag, error)
	ListAnnotations(context.Context, AnnotationListParams) ([]Annotation, error)
	GetProcessingStatus(context.Context, string, string) (ProcessingStatus, error)
	AddTags(context.Context, TagMutationParams) (TagMutationResult, error)
	RemoveTags(context.Context, TagMutationParams) (TagMutationResult, error)
	CreateAnnotation(context.Context, AnnotationCreateParams) (Annotation, error)
}

type Service struct {
	repository Repository
	random     io.Reader
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, random: rand.Reader}
}

func (service *Service) GetCollection(
	ctx context.Context,
	principal auth.Principal,
	collectionID string,
) (Collection, error) {
	if !principal.Can(auth.ScopeAssetsRead) {
		return Collection{}, ErrForbidden
	}
	collectionID, validID := identifier.NormalizeUUID(collectionID)
	if !validID {
		return Collection{}, ErrNotFound
	}
	result, err := service.repository.GetCollection(ctx, principal.WorkspaceID, collectionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Collection{}, ErrNotFound
		}
		return Collection{}, fmt.Errorf("get collection: %w", err)
	}
	return result, nil
}

func (service *Service) ListCollections(
	ctx context.Context,
	principal auth.Principal,
	input ListInput,
) (CollectionList, error) {
	if !principal.Can(auth.ScopeAssetsRead) {
		return CollectionList{}, ErrForbidden
	}
	limit, beforeCreatedAt, beforeID, err := normalizeListInput(input, "collections", principal.WorkspaceID)
	if err != nil {
		return CollectionList{}, err
	}
	items, err := service.repository.ListCollections(ctx, ListParams{
		WorkspaceID: principal.WorkspaceID, Limit: limit + 1,
		BeforeCreatedAt: beforeCreatedAt, BeforeID: beforeID,
	})
	if err != nil {
		return CollectionList{}, fmt.Errorf("list collections: %w", err)
	}
	result := CollectionList{Items: items}
	if len(result.Items) > limit {
		result.Items = append([]Collection(nil), result.Items[:limit]...)
		last := result.Items[len(result.Items)-1]
		cursor, encodeErr := encodeCursor("collections", principal.WorkspaceID, last.CreatedAt, last.ID)
		if encodeErr != nil {
			return CollectionList{}, fmt.Errorf("encode collection cursor: %w", encodeErr)
		}
		result.NextCursor = &cursor
	}
	if result.Items == nil {
		result.Items = make([]Collection, 0)
	}
	return result, nil
}

func (service *Service) ListTags(
	ctx context.Context,
	principal auth.Principal,
	input ListInput,
) (TagList, error) {
	if !principal.Can(auth.ScopeAssetsRead) {
		return TagList{}, ErrForbidden
	}
	limit, beforeCreatedAt, beforeID, err := normalizeListInput(input, "tags", principal.WorkspaceID)
	if err != nil {
		return TagList{}, err
	}
	items, err := service.repository.ListTags(ctx, ListParams{
		WorkspaceID: principal.WorkspaceID, Limit: limit + 1,
		BeforeCreatedAt: beforeCreatedAt, BeforeID: beforeID,
	})
	if err != nil {
		return TagList{}, fmt.Errorf("list tags: %w", err)
	}
	result := TagList{Items: items}
	if len(result.Items) > limit {
		result.Items = append([]Tag(nil), result.Items[:limit]...)
		last := result.Items[len(result.Items)-1]
		cursor, encodeErr := encodeCursor("tags", principal.WorkspaceID, last.CreatedAt, last.ID)
		if encodeErr != nil {
			return TagList{}, fmt.Errorf("encode tag cursor: %w", encodeErr)
		}
		result.NextCursor = &cursor
	}
	if result.Items == nil {
		result.Items = make([]Tag, 0)
	}
	return result, nil
}

func (service *Service) ListAssetTags(
	ctx context.Context,
	principal auth.Principal,
	input AssetTagListInput,
) (TagList, error) {
	if !principal.Can(auth.ScopeAssetsRead) {
		return TagList{}, ErrForbidden
	}
	assetID, validID := identifier.NormalizeUUID(input.AssetID)
	if !validID {
		return TagList{}, ErrNotFound
	}
	limit, beforeCreatedAt, beforeID, err := normalizeListInput(
		ListInput{Limit: input.Limit, Cursor: input.Cursor}, "asset_tags", assetID,
	)
	if err != nil {
		return TagList{}, err
	}
	items, err := service.repository.ListAssetTags(ctx, AssetTagListParams{
		WorkspaceID: principal.WorkspaceID, AssetID: assetID, Limit: limit + 1,
		BeforeCreatedAt: beforeCreatedAt, BeforeID: beforeID,
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return TagList{}, ErrNotFound
		}
		return TagList{}, fmt.Errorf("list asset tags: %w", err)
	}
	result := TagList{Items: items}
	if len(result.Items) > limit {
		result.Items = append([]Tag(nil), result.Items[:limit]...)
		last := result.Items[len(result.Items)-1]
		cursor, encodeErr := encodeCursor("asset_tags", assetID, last.CreatedAt, last.ID)
		if encodeErr != nil {
			return TagList{}, fmt.Errorf("encode asset tag cursor: %w", encodeErr)
		}
		result.NextCursor = &cursor
	}
	if result.Items == nil {
		result.Items = make([]Tag, 0)
	}
	return result, nil
}

func (service *Service) ListAnnotations(
	ctx context.Context,
	principal auth.Principal,
	input AnnotationListInput,
) (AnnotationList, error) {
	if !principal.Can(auth.ScopeAssetsRead) {
		return AnnotationList{}, ErrForbidden
	}
	assetID, validID := identifier.NormalizeUUID(input.AssetID)
	if !validID {
		return AnnotationList{}, ErrNotFound
	}
	limit, beforeCreatedAt, beforeID, err := normalizeListInput(
		ListInput{Limit: input.Limit, Cursor: input.Cursor}, "annotations", assetID,
	)
	if err != nil {
		return AnnotationList{}, err
	}
	items, err := service.repository.ListAnnotations(ctx, AnnotationListParams{
		WorkspaceID: principal.WorkspaceID, AssetID: assetID, Limit: limit + 1,
		BeforeCreatedAt: beforeCreatedAt, BeforeID: beforeID,
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return AnnotationList{}, ErrNotFound
		}
		return AnnotationList{}, fmt.Errorf("list annotations: %w", err)
	}
	result := AnnotationList{Items: items}
	if len(result.Items) > limit {
		result.Items = append([]Annotation(nil), result.Items[:limit]...)
		last := result.Items[len(result.Items)-1]
		cursor, encodeErr := encodeCursor("annotations", assetID, last.CreatedAt, last.ID)
		if encodeErr != nil {
			return AnnotationList{}, fmt.Errorf("encode annotation cursor: %w", encodeErr)
		}
		result.NextCursor = &cursor
	}
	if result.Items == nil {
		result.Items = make([]Annotation, 0)
	}
	return result, nil
}

func (service *Service) GetProcessingStatus(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
) (ProcessingStatus, error) {
	if !principal.Can(auth.ScopeAssetsRead) {
		return ProcessingStatus{}, ErrForbidden
	}
	assetID, validID := identifier.NormalizeUUID(assetID)
	if !validID {
		return ProcessingStatus{}, ErrNotFound
	}
	result, err := service.repository.GetProcessingStatus(ctx, principal.WorkspaceID, assetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ProcessingStatus{}, ErrNotFound
		}
		return ProcessingStatus{}, fmt.Errorf("get processing status: %w", err)
	}
	if result.Jobs == nil {
		result.Jobs = make([]ProcessingJob, 0)
	}
	return result, nil
}

func (service *Service) AddTags(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	input TagMutationInput,
	requestID string,
) (TagMutationResult, error) {
	return service.mutateTags(ctx, principal, assetID, input, requestID, true)
}

func (service *Service) RemoveTags(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	input TagMutationInput,
	requestID string,
) (TagMutationResult, error) {
	return service.mutateTags(ctx, principal, assetID, input, requestID, false)
}

func (service *Service) mutateTags(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	input TagMutationInput,
	requestID string,
	add bool,
) (TagMutationResult, error) {
	if !principal.Can(auth.ScopeMetadataWrite) {
		return TagMutationResult{}, ErrForbidden
	}
	assetID, validID := identifier.NormalizeUUID(assetID)
	tagIDs, validTags := normalizeTagIDs(input.TagIDs)
	if !validID || !validTags || strings.TrimSpace(requestID) != requestID || requestID == "" || len(requestID) > 200 {
		return TagMutationResult{}, ErrInvalidInput
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return TagMutationResult{}, fmt.Errorf("generate tag audit identifier: %w", err)
	}
	params := TagMutationParams{
		AuditID: auditID, WorkspaceID: principal.WorkspaceID, AssetID: assetID,
		ActorID: principal.UserID, ActorType: organizationActorType(principal),
		CredentialID: principal.CredentialID, RequestID: requestID, TagIDs: tagIDs,
	}
	var result TagMutationResult
	if add {
		result, err = service.repository.AddTags(ctx, params)
	} else {
		result, err = service.repository.RemoveTags(ctx, params)
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return TagMutationResult{}, ErrNotFound
		}
		return TagMutationResult{}, fmt.Errorf("mutate asset tags: %w", err)
	}
	if result.TagIDs == nil {
		result.TagIDs = make([]string, 0)
	}
	return result, nil
}

func (service *Service) CreateAnnotation(
	ctx context.Context,
	principal auth.Principal,
	assetID string,
	input AnnotationCreateInput,
	requestID string,
) (Annotation, error) {
	if !principal.Can(auth.ScopeMetadataWrite) {
		return Annotation{}, ErrForbidden
	}
	assetID, validID := identifier.NormalizeUUID(assetID)
	input.Kind = strings.TrimSpace(input.Kind)
	input.Body = strings.TrimSpace(input.Body)
	if !validID || !validAnnotation(input) || strings.TrimSpace(requestID) != requestID || requestID == "" || len(requestID) > 200 {
		return Annotation{}, ErrInvalidInput
	}
	annotationID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Annotation{}, fmt.Errorf("generate annotation identifier: %w", err)
	}
	auditID, err := identifier.NewUUIDFrom(service.random)
	if err != nil {
		return Annotation{}, fmt.Errorf("generate annotation audit identifier: %w", err)
	}
	created, err := service.repository.CreateAnnotation(ctx, AnnotationCreateParams{
		ID: annotationID, AuditID: auditID, WorkspaceID: principal.WorkspaceID,
		AssetID: assetID, ActorID: principal.UserID, ActorType: organizationActorType(principal),
		CredentialID: principal.CredentialID, RequestID: requestID,
		Kind: input.Kind, StartMS: input.StartMS, EndMS: input.EndMS, Body: input.Body,
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Annotation{}, ErrNotFound
		}
		return Annotation{}, fmt.Errorf("create annotation: %w", err)
	}
	return created, nil
}

func normalizeTagIDs(values []string) ([]string, bool) {
	if len(values) < 1 || len(values) > 100 {
		return nil, false
	}
	results := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value, validID := identifier.NormalizeUUID(value)
		if !validID {
			return nil, false
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, false
		}
		seen[value] = struct{}{}
		results = append(results, value)
	}
	slices.Sort(results)
	return results, true
}

func validAnnotation(input AnnotationCreateInput) bool {
	if input.Kind != "bookmark" && input.Kind != "note" {
		return false
	}
	if input.StartMS < 0 || (input.EndMS != nil && *input.EndMS <= input.StartMS) {
		return false
	}
	if input.Kind == "note" && input.Body == "" {
		return false
	}
	if utf8.RuneCountInString(input.Body) > 4000 {
		return false
	}
	for _, character := range input.Body {
		if unicode.IsControl(character) && character != '\n' && character != '\r' && character != '\t' {
			return false
		}
	}
	return true
}

func organizationActorType(principal auth.Principal) string {
	if principal.Role == "agent" || principal.CredentialType == "api_key" {
		return "agent"
	}
	return "user"
}

type listCursor struct {
	Kind        string `json:"kind"`
	BindingHash string `json:"binding_hash"`
	CreatedAt   string `json:"created_at"`
	ID          string `json:"id"`
}

func normalizeListInput(input ListInput, kind, binding string) (int, *time.Time, string, error) {
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	if limit < 1 || limit > maxListLimit {
		return 0, nil, "", ErrInvalidInput
	}
	createdAt, id, err := decodeCursor(input.Cursor, kind, binding)
	if err != nil {
		return 0, nil, "", ErrInvalidInput
	}
	return limit, createdAt, id, nil
}

func encodeCursor(kind, binding string, createdAt time.Time, id string) (string, error) {
	payload, err := json.Marshal(listCursor{
		Kind: kind, BindingHash: bindingDigest(binding),
		CreatedAt: createdAt.UTC().Format(time.RFC3339Nano), ID: id,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeCursor(value, kind, binding string) (*time.Time, string, error) {
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
	if err != nil || cursor.Kind != kind || cursor.BindingHash != bindingDigest(binding) {
		return nil, "", ErrInvalidInput
	}
	id, validID := identifier.NormalizeUUID(cursor.ID)
	if !validID {
		return nil, "", ErrInvalidInput
	}
	createdAt = createdAt.UTC()
	return &createdAt, id, nil
}

func bindingDigest(binding string) string {
	digest := sha256.Sum256([]byte(binding))
	return hex.EncodeToString(digest[:])
}
