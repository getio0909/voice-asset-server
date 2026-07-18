package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/organization"
)

func TestListCollectionsPassesPaginationAndRecordsAudit(t *testing.T) {
	nextCursor := "next-collection"
	service := &fakeOrganizationService{collections: organization.CollectionList{
		Items:      []organization.Collection{{ID: "20000000-0000-4000-8000-000000000091", Name: "Interviews"}},
		NextCursor: &nextCursor,
	}}
	auditService := &fakeAuditService{}
	handler := organizationHTTPHandler(service, auditService)
	request := authenticatedOrganizationRequest(http.MethodGet, "/api/v1/collections?limit=25&cursor=previous")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if service.collectionInput != (organization.ListInput{Limit: 25, Cursor: "previous"}) {
		t.Fatalf("collection input = %+v", service.collectionInput)
	}
	var response organization.CollectionList
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Items) != 1 || response.NextCursor == nil {
		t.Fatalf("decode collection list = (%+v, %v)", response, err)
	}
	if auditService.calls != 1 || auditService.input.Action != "collection.listed" || auditService.input.TargetType != "collection" {
		t.Fatalf("audit input = %+v, calls = %d", auditService.input, auditService.calls)
	}
}

func TestGetCollectionReturnsVersionedResourceAndRecordsAudit(t *testing.T) {
	const collectionID = "20000000-0000-4000-8000-000000000091"
	service := &fakeOrganizationService{collection: organization.Collection{
		ID: collectionID, Name: "Interviews", Version: 3, AssetCount: 2,
	}}
	auditService := &fakeAuditService{}
	handler := organizationHTTPHandler(service, auditService)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedOrganizationRequest(
		http.MethodGet, "/api/v1/collections/"+collectionID,
	))
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"3"` || service.collectionID != collectionID {
		t.Fatalf("response/id = %d %v / %q: %s", recorder.Code, recorder.Header(), service.collectionID, recorder.Body.String())
	}
	if auditService.input.Action != "collection.read" || auditService.input.TargetID != collectionID {
		t.Fatalf("audit input = %+v", auditService.input)
	}
}

func TestListTagsPassesPaginationAndRecordsAudit(t *testing.T) {
	service := &fakeOrganizationService{tags: organization.TagList{
		Items: []organization.Tag{{ID: "30000000-0000-4000-8000-000000000091", Name: "Important"}},
	}}
	auditService := &fakeAuditService{}
	handler := organizationHTTPHandler(service, auditService)
	request := authenticatedOrganizationRequest(http.MethodGet, "/api/v1/tags?limit=10")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || service.tagInput.Limit != 10 {
		t.Fatalf("tag response/input = %d / %+v: %s", recorder.Code, service.tagInput, recorder.Body.String())
	}
	if auditService.calls != 1 || auditService.input.Action != "tag.listed" || auditService.input.TargetType != "tag" {
		t.Fatalf("audit input = %+v, calls = %d", auditService.input, auditService.calls)
	}
}

func TestAssetOrganizationRoutesReturnReadModelsAndAudit(t *testing.T) {
	nextCursor := "next-annotation"
	service := &fakeOrganizationService{
		assetTags: organization.TagList{
			Items: []organization.Tag{{ID: "30000000-0000-4000-8000-000000000091", Name: "Important"}},
		},
		annotations: organization.AnnotationList{
			Items:      []organization.Annotation{{ID: "40000000-0000-4000-8000-000000000091", AssetID: httpAssetID, StartMS: 1250}},
			NextCursor: &nextCursor,
		},
		processingStatus: organization.ProcessingStatus{
			AssetID: httpAssetID, AssetStatus: "processing", Active: true,
			Jobs: []organization.ProcessingJob{{ID: httpJobID, State: "queued"}},
		},
	}
	tagAudit := &fakeAuditService{}
	handler := organizationHTTPHandler(service, tagAudit)
	request := authenticatedOrganizationRequest(http.MethodGet, "/api/v1/assets/"+httpAssetID+"/tags?limit=5")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.assetTagInput != (organization.AssetTagListInput{AssetID: httpAssetID, Limit: 5}) {
		t.Fatalf("asset tag response/input = %d / %+v: %s", recorder.Code, service.assetTagInput, recorder.Body.String())
	}
	if tagAudit.input.Action != "asset.tags.listed" || tagAudit.input.TargetID != httpAssetID {
		t.Fatalf("asset tag audit = %+v", tagAudit.input)
	}
	annotationAudit := &fakeAuditService{}
	handler = organizationHTTPHandler(service, annotationAudit)
	request = authenticatedOrganizationRequest(http.MethodGet, "/api/v1/assets/"+httpAssetID+"/annotations?limit=5&cursor=before")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("annotation status = %d: %s", recorder.Code, recorder.Body.String())
	}
	wantInput := organization.AnnotationListInput{AssetID: httpAssetID, Limit: 5, Cursor: "before"}
	if service.annotationInput != wantInput {
		t.Fatalf("annotation input = %+v, want %+v", service.annotationInput, wantInput)
	}
	if annotationAudit.input.Action != "annotation.listed" || annotationAudit.input.TargetID != httpAssetID {
		t.Fatalf("annotation audit = %+v", annotationAudit.input)
	}

	statusAudit := &fakeAuditService{}
	handler = organizationHTTPHandler(service, statusAudit)
	request = authenticatedOrganizationRequest(http.MethodGet, "/api/v1/assets/"+httpAssetID+"/processing-status")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || service.processingAssetID != httpAssetID {
		t.Fatalf("processing response/id = %d / %q: %s", recorder.Code, service.processingAssetID, recorder.Body.String())
	}
	var status organization.ProcessingStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil || !status.Active || len(status.Jobs) != 1 {
		t.Fatalf("decode processing status = (%+v, %v)", status, err)
	}
	if statusAudit.input.Action != "asset.processing_status.read" || statusAudit.input.TargetID != httpAssetID {
		t.Fatalf("processing audit = %+v", statusAudit.input)
	}
}

func TestAssetOrganizationMutationsDecodeBodiesAndPreserveRequestIDs(t *testing.T) {
	const (
		tagID        = "50000000-0000-4000-8000-000000000091"
		annotationID = "60000000-0000-4000-8000-000000000091"
	)
	service := &fakeOrganizationService{
		tagMutation: organization.TagMutationResult{
			AssetID: httpAssetID, TagIDs: []string{tagID}, ChangedCount: 1,
		},
		createdAnnotation: organization.Annotation{
			ID: annotationID, AssetID: httpAssetID, Kind: "note", StartMS: 1250,
			Body: "Decision", Version: 1,
		},
	}
	handler := organizationHTTPHandler(service, nil)

	request := authenticatedOrganizationJSONRequest(
		http.MethodPost,
		"/api/v1/assets/"+httpAssetID+"/tags",
		`{"tag_ids":["`+tagID+`"]}`,
		"organization-add-tags",
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.addTagCalls != 1 ||
		service.tagMutationAssetID != httpAssetID || service.tagMutationRequestID != "organization-add-tags" ||
		len(service.tagMutationInput.TagIDs) != 1 || service.tagMutationInput.TagIDs[0] != tagID {
		t.Fatalf("add tags response/call = %d / %+v: %s", recorder.Code, service, recorder.Body.String())
	}

	request = authenticatedOrganizationJSONRequest(
		http.MethodDelete,
		"/api/v1/assets/"+httpAssetID+"/tags",
		`{"tag_ids":["`+tagID+`"]}`,
		"organization-remove-tags",
	)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || service.removeTagCalls != 1 || service.tagMutationRequestID != "organization-remove-tags" {
		t.Fatalf("remove tags response/call = %d / %+v: %s", recorder.Code, service, recorder.Body.String())
	}

	request = authenticatedOrganizationJSONRequest(
		http.MethodPost,
		"/api/v1/assets/"+httpAssetID+"/annotations",
		`{"kind":"note","start_ms":1250,"body":"Decision"}`,
		"organization-create-annotation",
	)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || service.createAnnotationCalls != 1 ||
		service.annotationCreateAssetID != httpAssetID ||
		service.annotationCreateRequestID != "organization-create-annotation" ||
		service.annotationCreateInput.Kind != "note" ||
		recorder.Header().Get("Location") != "/api/v1/assets/"+httpAssetID+"/annotations/"+annotationID ||
		recorder.Header().Get("ETag") != `"1"` {
		t.Fatalf("create annotation response/call = %d %v / %+v: %s", recorder.Code, recorder.Header(), service, recorder.Body.String())
	}
}

func TestAssetOrganizationMutationsRejectInvalidBodiesAndMethods(t *testing.T) {
	service := &fakeOrganizationService{}
	handler := organizationHTTPHandler(service, nil)

	request := authenticatedOrganizationJSONRequest(
		http.MethodPost,
		"/api/v1/assets/"+httpAssetID+"/tags",
		`{"tag_ids":[],"unexpected":true}`,
		"organization-invalid-tags",
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid body response = %d %s", recorder.Code, recorder.Body.String())
	}
	if service.calls != 0 {
		t.Fatalf("service calls after invalid body = %d", service.calls)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedOrganizationRequest(
		http.MethodPut, "/api/v1/assets/"+httpAssetID+"/tags",
	))
	if recorder.Code != http.StatusMethodNotAllowed || recorder.Header().Get("Allow") != "GET, POST, DELETE" {
		t.Fatalf("method response = %d %v", recorder.Code, recorder.Header())
	}
}

func TestOrganizationListsRejectInvalidOrRepeatedParameters(t *testing.T) {
	for _, target := range []string{
		"/api/v1/collections?limit=nope",
		"/api/v1/tags?limit=1&limit=2",
		"/api/v1/assets/" + httpAssetID + "/annotations?cursor=one&cursor=two",
	} {
		t.Run(target, func(t *testing.T) {
			service := &fakeOrganizationService{}
			handler := organizationHTTPHandler(service, nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, authenticatedOrganizationRequest(http.MethodGet, target))
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"invalid_request"`) {
				t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
			}
			if service.calls != 0 {
				t.Fatalf("service calls = %d, want 0", service.calls)
			}
		})
	}
}

func TestOrganizationRoutesMapDomainErrorsAndMethods(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		method     string
		service    *fakeOrganizationService
		wantStatus int
	}{
		{
			name: "forbidden collection", target: "/api/v1/collections", method: http.MethodGet,
			service: &fakeOrganizationService{collectionErr: organization.ErrForbidden}, wantStatus: http.StatusForbidden,
		},
		{
			name: "missing annotation asset", target: "/api/v1/assets/" + httpAssetID + "/annotations", method: http.MethodGet,
			service: &fakeOrganizationService{annotationErr: organization.ErrNotFound}, wantStatus: http.StatusNotFound,
		},
		{
			name: "method", target: "/api/v1/tags", method: http.MethodPost,
			service: &fakeOrganizationService{}, wantStatus: http.StatusMethodNotAllowed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := organizationHTTPHandler(test.service, nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, authenticatedOrganizationRequest(test.method, test.target))
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestOrganizationReadsFailClosedWhenAuditCannotBePersisted(t *testing.T) {
	service := &fakeOrganizationService{processingStatus: organization.ProcessingStatus{
		AssetID: httpAssetID, Jobs: []organization.ProcessingJob{},
	}}
	handler := organizationHTTPHandler(service, &fakeAuditService{err: errors.New("database unavailable")})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authenticatedOrganizationRequest(
		http.MethodGet, "/api/v1/assets/"+httpAssetID+"/processing-status",
	))
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func organizationHTTPHandler(service OrganizationService, auditService AuditService) http.Handler {
	return NewApplicationHandler(Options{
		AuthService: &fakeAuthService{principal: auth.Principal{
			UserID: "20000000-0000-4000-8000-000000000091", WorkspaceID: "10000000-0000-4000-8000-000000000091",
			Scopes: []string{auth.ScopeAssetsRead, auth.ScopeMetadataWrite},
		}},
		OrganizationService: service,
		AuditService:        auditService,
	})
}

func authenticatedOrganizationRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	return request
}

func authenticatedOrganizationJSONRequest(method, target, body, requestID string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", requestID)
	return request
}

type fakeOrganizationService struct {
	collection                organization.Collection
	collectionID              string
	collectionGetErr          error
	collections               organization.CollectionList
	collectionInput           organization.ListInput
	collectionErr             error
	tags                      organization.TagList
	tagInput                  organization.ListInput
	tagErr                    error
	assetTags                 organization.TagList
	assetTagInput             organization.AssetTagListInput
	assetTagErr               error
	annotations               organization.AnnotationList
	annotationInput           organization.AnnotationListInput
	annotationErr             error
	processingStatus          organization.ProcessingStatus
	processingAssetID         string
	processingErr             error
	tagMutation               organization.TagMutationResult
	tagMutationAssetID        string
	tagMutationInput          organization.TagMutationInput
	tagMutationRequestID      string
	tagMutationErr            error
	addTagCalls               int
	removeTagCalls            int
	createdAnnotation         organization.Annotation
	annotationCreateAssetID   string
	annotationCreateInput     organization.AnnotationCreateInput
	annotationCreateRequestID string
	annotationCreateErr       error
	createAnnotationCalls     int
	calls                     int
}

func (service *fakeOrganizationService) GetCollection(
	_ context.Context,
	_ auth.Principal,
	collectionID string,
) (organization.Collection, error) {
	service.calls++
	service.collectionID = collectionID
	return service.collection, service.collectionGetErr
}

func (service *fakeOrganizationService) ListCollections(
	_ context.Context,
	_ auth.Principal,
	input organization.ListInput,
) (organization.CollectionList, error) {
	service.calls++
	service.collectionInput = input
	return service.collections, service.collectionErr
}

func (service *fakeOrganizationService) ListTags(
	_ context.Context,
	_ auth.Principal,
	input organization.ListInput,
) (organization.TagList, error) {
	service.calls++
	service.tagInput = input
	return service.tags, service.tagErr
}

func (service *fakeOrganizationService) ListAssetTags(
	_ context.Context,
	_ auth.Principal,
	input organization.AssetTagListInput,
) (organization.TagList, error) {
	service.calls++
	service.assetTagInput = input
	return service.assetTags, service.assetTagErr
}

func (service *fakeOrganizationService) ListAnnotations(
	_ context.Context,
	_ auth.Principal,
	input organization.AnnotationListInput,
) (organization.AnnotationList, error) {
	service.calls++
	service.annotationInput = input
	return service.annotations, service.annotationErr
}

func (service *fakeOrganizationService) GetProcessingStatus(
	_ context.Context,
	_ auth.Principal,
	assetID string,
) (organization.ProcessingStatus, error) {
	service.calls++
	service.processingAssetID = assetID
	return service.processingStatus, service.processingErr
}

func (service *fakeOrganizationService) AddTags(
	_ context.Context,
	_ auth.Principal,
	assetID string,
	input organization.TagMutationInput,
	requestID string,
) (organization.TagMutationResult, error) {
	service.calls++
	service.addTagCalls++
	service.tagMutationAssetID = assetID
	service.tagMutationInput = input
	service.tagMutationRequestID = requestID
	return service.tagMutation, service.tagMutationErr
}

func (service *fakeOrganizationService) RemoveTags(
	_ context.Context,
	_ auth.Principal,
	assetID string,
	input organization.TagMutationInput,
	requestID string,
) (organization.TagMutationResult, error) {
	service.calls++
	service.removeTagCalls++
	service.tagMutationAssetID = assetID
	service.tagMutationInput = input
	service.tagMutationRequestID = requestID
	return service.tagMutation, service.tagMutationErr
}

func (service *fakeOrganizationService) CreateAnnotation(
	_ context.Context,
	_ auth.Principal,
	assetID string,
	input organization.AnnotationCreateInput,
	requestID string,
) (organization.Annotation, error) {
	service.calls++
	service.createAnnotationCalls++
	service.annotationCreateAssetID = assetID
	service.annotationCreateInput = input
	service.annotationCreateRequestID = requestID
	return service.createdAnnotation, service.annotationCreateErr
}

var _ OrganizationService = (*fakeOrganizationService)(nil)
