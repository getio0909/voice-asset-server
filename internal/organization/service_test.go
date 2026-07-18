package organization

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestCollectionAndTagListsUseBoundStableCursors(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	repository := &fakeRepository{collections: []Collection{
		{ID: "30000000-0000-4000-8000-000000000002", CreatedAt: now},
		{ID: "30000000-0000-4000-8000-000000000001", CreatedAt: now.Add(-time.Minute)},
	}}
	service := NewService(repository)
	principal := auth.Principal{
		WorkspaceID: "10000000-0000-4000-8000-000000000001",
		Scopes:      []string{auth.ScopeAssetsRead},
	}

	first, err := service.ListCollections(context.Background(), principal, ListInput{Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("ListCollections() = (%+v, %v)", first, err)
	}
	if repository.collectionParams.Limit != 2 {
		t.Fatalf("repository limit = %d, want 2", repository.collectionParams.Limit)
	}
	repository.collections = nil
	if _, err := service.ListCollections(context.Background(), principal, ListInput{
		Limit: 1, Cursor: *first.NextCursor,
	}); err != nil {
		t.Fatalf("ListCollections(cursor) error = %v", err)
	}
	if repository.collectionParams.BeforeCreatedAt == nil ||
		repository.collectionParams.BeforeID != first.Items[0].ID {
		t.Fatalf("decoded collection cursor = %+v", repository.collectionParams)
	}
	if _, err := service.ListTags(context.Background(), principal, ListInput{
		Cursor: *first.NextCursor,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ListTags(collection cursor) error = %v", err)
	}
}

func TestAnnotationCursorIsBoundToAsset(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	const assetID = "30000000-0000-4000-8000-000000000011"
	repository := &fakeRepository{annotations: []Annotation{
		{ID: "40000000-0000-4000-8000-000000000012", AssetID: assetID, CreatedAt: now},
		{ID: "40000000-0000-4000-8000-000000000011", AssetID: assetID, CreatedAt: now.Add(-time.Minute)},
	}}
	service := NewService(repository)
	principal := auth.Principal{WorkspaceID: "10000000-0000-4000-8000-000000000011", Scopes: []string{auth.ScopeAssetsRead}}
	first, err := service.ListAnnotations(context.Background(), principal, AnnotationListInput{AssetID: assetID, Limit: 1})
	if err != nil || first.NextCursor == nil {
		t.Fatalf("ListAnnotations() = (%+v, %v)", first, err)
	}
	_, err = service.ListAnnotations(context.Background(), principal, AnnotationListInput{
		AssetID: "30000000-0000-4000-8000-000000000099", Cursor: *first.NextCursor,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ListAnnotations(other asset cursor) error = %v", err)
	}
	if _, err := service.ListAnnotations(context.Background(), principal, AnnotationListInput{AssetID: "bad"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListAnnotations(bad ID) error = %v", err)
	}
}

func TestAssetTagCursorIsBoundToAsset(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	const assetID = "30000000-0000-4000-8000-000000000019"
	repository := &fakeRepository{assetTags: []Tag{
		{ID: "50000000-0000-4000-8000-000000000012", CreatedAt: now},
		{ID: "50000000-0000-4000-8000-000000000011", CreatedAt: now.Add(-time.Minute)},
	}}
	service := NewService(repository)
	principal := auth.Principal{WorkspaceID: "10000000-0000-4000-8000-000000000019", Scopes: []string{auth.ScopeAssetsRead}}
	first, err := service.ListAssetTags(context.Background(), principal, AssetTagListInput{AssetID: assetID, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("ListAssetTags() = (%+v, %v)", first, err)
	}
	if repository.assetTagParams.Limit != 2 || repository.assetTagParams.AssetID != assetID {
		t.Fatalf("asset tag params = %+v", repository.assetTagParams)
	}
	if _, err := service.ListAssetTags(context.Background(), principal, AssetTagListInput{
		AssetID: "30000000-0000-4000-8000-000000000099", Cursor: *first.NextCursor,
	}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ListAssetTags(other asset cursor) error = %v", err)
	}
	if _, err := service.ListAssetTags(context.Background(), principal, AssetTagListInput{AssetID: "bad"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListAssetTags(bad ID) error = %v", err)
	}
}

func TestOrganizationReadsRequireAssetReadScope(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	principal := auth.Principal{Scopes: []string{auth.ScopeTranscriptsRead}}
	if _, err := service.GetCollection(context.Background(), principal, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetCollection() error = %v", err)
	}
	if _, err := service.ListCollections(context.Background(), principal, ListInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListCollections() error = %v", err)
	}
	if _, err := service.ListTags(context.Background(), principal, ListInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListTags() error = %v", err)
	}
	if _, err := service.ListAssetTags(context.Background(), principal, AssetTagListInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListAssetTags() error = %v", err)
	}
	if _, err := service.ListAnnotations(context.Background(), principal, AnnotationListInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListAnnotations() error = %v", err)
	}
	if _, err := service.GetProcessingStatus(context.Background(), principal, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetProcessingStatus() error = %v", err)
	}
	if _, err := service.AddTags(context.Background(), principal, "", TagMutationInput{}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("AddTags() error = %v", err)
	}
	if _, err := service.RemoveTags(context.Background(), principal, "", TagMutationInput{}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("RemoveTags() error = %v", err)
	}
	if _, err := service.CreateAnnotation(context.Background(), principal, "", AnnotationCreateInput{}, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateAnnotation() error = %v", err)
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls = %d, want 0", repository.calls)
	}
}

func TestProcessingStatusNormalizesEmptyJobsAndErrors(t *testing.T) {
	const assetID = "30000000-0000-4000-8000-000000000021"
	repository := &fakeRepository{processingStatus: ProcessingStatus{AssetID: assetID, AssetStatus: "ready"}}
	service := NewService(repository)
	principal := auth.Principal{WorkspaceID: "10000000-0000-4000-8000-000000000021", Scopes: []string{auth.ScopeAssetsRead}}
	result, err := service.GetProcessingStatus(context.Background(), principal, assetID)
	if err != nil || result.Jobs == nil {
		t.Fatalf("GetProcessingStatus() = (%+v, %v)", result, err)
	}
	repository.processingErr = ErrNotFound
	if _, err := service.GetProcessingStatus(context.Background(), principal, assetID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProcessingStatus(not found) error = %v", err)
	}
}

func TestMetadataMutationsValidateNormalizeAndPreserveAgentAttribution(t *testing.T) {
	const (
		workspaceID  = "10000000-0000-4000-8000-000000000031"
		userID       = "20000000-0000-4000-8000-000000000031"
		credentialID = "30000000-0000-4000-8000-000000000031"
		assetID      = "40000000-0000-4000-8000-000000000031"
		firstTagID   = "50000000-0000-4000-8000-000000000031"
		secondTagID  = "50000000-0000-4000-8000-000000000032"
	)
	endMS := int64(2500)
	repository := &fakeRepository{
		tagMutation: TagMutationResult{AssetID: assetID, ChangedCount: 1},
		annotation: Annotation{
			ID: "60000000-0000-4000-8000-000000000031", AssetID: assetID,
		},
	}
	service := NewService(repository)
	principal := auth.Principal{
		UserID: userID, WorkspaceID: workspaceID, Role: "agent",
		CredentialType: "api_key", CredentialID: credentialID,
		Scopes: []string{auth.ScopeMetadataWrite},
	}

	added, err := service.AddTags(context.Background(), principal, assetID, TagMutationInput{
		TagIDs: []string{secondTagID, firstTagID},
	}, "request-add-tags")
	if err != nil || added.TagIDs == nil {
		t.Fatalf("AddTags() = (%+v, %v)", added, err)
	}
	if got := repository.tagMutationParams.TagIDs; len(got) != 2 || got[0] != firstTagID || got[1] != secondTagID {
		t.Fatalf("normalized tag IDs = %v", got)
	}
	if repository.tagMutationParams.ActorType != "agent" ||
		repository.tagMutationParams.ActorID != userID ||
		repository.tagMutationParams.CredentialID != credentialID {
		t.Fatalf("tag attribution = %+v", repository.tagMutationParams)
	}

	created, err := service.CreateAnnotation(context.Background(), principal, assetID, AnnotationCreateInput{
		Kind: " note ", StartMS: 1250, EndMS: &endMS, Body: " decision ",
	}, "request-create-annotation")
	if err != nil || created.AssetID != assetID {
		t.Fatalf("CreateAnnotation() = (%+v, %v)", created, err)
	}
	if repository.annotationCreateParams.Kind != "note" ||
		repository.annotationCreateParams.Body != "decision" ||
		repository.annotationCreateParams.ActorType != "agent" ||
		repository.annotationCreateParams.CredentialID != credentialID {
		t.Fatalf("annotation params = %+v", repository.annotationCreateParams)
	}

	if _, err := service.RemoveTags(context.Background(), principal, assetID, TagMutationInput{
		TagIDs: []string{firstTagID, firstTagID},
	}, "request-remove-tags"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("RemoveTags(duplicate IDs) error = %v", err)
	}
	if _, err := service.CreateAnnotation(context.Background(), principal, assetID, AnnotationCreateInput{
		Kind: "note", StartMS: 1250,
	}, "request-empty-note"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateAnnotation(empty note) error = %v", err)
	}
}

type fakeRepository struct {
	collection             Collection
	getCollectionErr       error
	collections            []Collection
	collectionParams       ListParams
	collectionErr          error
	tags                   []Tag
	tagParams              ListParams
	tagErr                 error
	assetTags              []Tag
	assetTagParams         AssetTagListParams
	assetTagErr            error
	annotations            []Annotation
	annotationParams       AnnotationListParams
	annotationErr          error
	processingStatus       ProcessingStatus
	processingErr          error
	tagMutation            TagMutationResult
	tagMutationParams      TagMutationParams
	tagMutationErr         error
	annotation             Annotation
	annotationCreateParams AnnotationCreateParams
	annotationCreateErr    error
	calls                  int
}

func (repository *fakeRepository) GetCollection(context.Context, string, string) (Collection, error) {
	repository.calls++
	return repository.collection, repository.getCollectionErr
}

func (repository *fakeRepository) ListCollections(_ context.Context, params ListParams) ([]Collection, error) {
	repository.calls++
	repository.collectionParams = params
	return repository.collections, repository.collectionErr
}

func (repository *fakeRepository) ListTags(_ context.Context, params ListParams) ([]Tag, error) {
	repository.calls++
	repository.tagParams = params
	return repository.tags, repository.tagErr
}

func (repository *fakeRepository) ListAssetTags(_ context.Context, params AssetTagListParams) ([]Tag, error) {
	repository.calls++
	repository.assetTagParams = params
	return repository.assetTags, repository.assetTagErr
}

func (repository *fakeRepository) ListAnnotations(_ context.Context, params AnnotationListParams) ([]Annotation, error) {
	repository.calls++
	repository.annotationParams = params
	return repository.annotations, repository.annotationErr
}

func (repository *fakeRepository) GetProcessingStatus(context.Context, string, string) (ProcessingStatus, error) {
	repository.calls++
	return repository.processingStatus, repository.processingErr
}

func (repository *fakeRepository) AddTags(_ context.Context, params TagMutationParams) (TagMutationResult, error) {
	repository.calls++
	repository.tagMutationParams = params
	return repository.tagMutation, repository.tagMutationErr
}

func (repository *fakeRepository) RemoveTags(_ context.Context, params TagMutationParams) (TagMutationResult, error) {
	repository.calls++
	repository.tagMutationParams = params
	return repository.tagMutation, repository.tagMutationErr
}

func (repository *fakeRepository) CreateAnnotation(_ context.Context, params AnnotationCreateParams) (Annotation, error) {
	repository.calls++
	repository.annotationCreateParams = params
	return repository.annotation, repository.annotationCreateErr
}
