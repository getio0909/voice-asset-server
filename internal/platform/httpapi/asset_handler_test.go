package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestCreateAssetRequiresIdempotencyAndReturnsResourceHeaders(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
	}}
	assetService := &fakeAssetService{created: asset.Asset{
		ID: "asset-1", WorkspaceID: "workspace-1", Title: "Recording", Language: "en",
		Status: "draft", Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, AssetService: assetService,
		PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/assets",
		strings.NewReader(`{"title":"Recording","language":"en"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-asset-1")
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/v1/assets/asset-1" || recorder.Header().Get("ETag") != `"1"` {
		t.Fatalf("resource headers = %v", recorder.Header())
	}
	if assetService.idempotencyKey != "create-asset-1" || assetService.input.Title != "Recording" {
		t.Fatalf("service args = %+v/%q", assetService.input, assetService.idempotencyKey)
	}
	var response asset.Asset
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.ID != "asset-1" {
		t.Fatalf("decode asset = (%+v, %v)", response, err)
	}
}

func TestCreateAssetRejectsMissingIdempotencyKey(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
	}}
	assetService := &fakeAssetService{createErr: asset.ErrInvalidInput}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, AssetService: assetService,
		PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/assets",
		strings.NewReader(`{"title":"Recording","language":"en"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestUpdateAssetMetadataRequiresVersionAndFullReplacement(t *testing.T) {
	const collectionID = "50000000-0000-4000-8000-000000000081"
	collection := collectionID
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeMetadataWrite},
	}}
	assetService := &fakeAssetService{updated: asset.Asset{
		ID: httpAssetID, WorkspaceID: "workspace-1", CollectionID: &collection,
		Title: "Planning call", Language: "zh-CN", Status: "ready", Version: 4,
	}}
	handler := NewApplicationHandler(Options{AuthService: authService, AssetService: assetService})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/assets/"+httpAssetID+"/metadata",
		strings.NewReader(`{"title":"Planning call","language":"zh-cn","collection_id":"`+collectionID+`"}`))
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"3"`)
	request.Header.Set("X-Request-ID", "asset-metadata-update")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"4"` {
		t.Fatalf("response = %d %v: %s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if assetService.updateAssetID != httpAssetID || assetService.expectedVersion != 3 ||
		assetService.updateInput.CollectionID == nil || *assetService.updateInput.CollectionID != collectionID ||
		assetService.updateRequestID != "asset-metadata-update" {
		t.Fatalf("service args = id=%q version=%d input=%+v request=%q",
			assetService.updateAssetID, assetService.expectedVersion, assetService.updateInput, assetService.updateRequestID)
	}

	assetService = &fakeAssetService{}
	handler = NewApplicationHandler(Options{AuthService: authService, AssetService: assetService})
	request = httptest.NewRequest(http.MethodPut, "/api/v1/assets/"+httpAssetID+"/metadata",
		strings.NewReader(`{"title":"Planning call","language":"en"}`))
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"3"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || assetService.updateCalls != 0 {
		t.Fatalf("missing collection response/calls = %d/%d: %s", recorder.Code, assetService.updateCalls, recorder.Body.String())
	}
}

func TestUpdateAssetMetadataMapsPreconditionAndConflict(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeMetadataWrite},
	}}
	assetService := &fakeAssetService{updateErr: asset.ErrConflict}
	handler := NewApplicationHandler(Options{AuthService: authService, AssetService: assetService})
	newRequest := func() *http.Request {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/assets/"+httpAssetID+"/metadata",
			strings.NewReader(`{"title":"Planning call","language":"en","collection_id":null}`))
		request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
		request.Header.Set("Content-Type", "application/json")
		return request
	}

	request := newRequest()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired || assetService.updateCalls != 0 {
		t.Fatalf("missing precondition response/calls = %d/%d", recorder.Code, assetService.updateCalls)
	}

	request = newRequest()
	request.Header.Set("If-Match", `"2"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"conflict"`) {
		t.Fatalf("conflict response = %d %s", recorder.Code, recorder.Body.String())
	}
	if assetService.updateInput.CollectionID != nil {
		t.Fatalf("explicit null collection = %+v", assetService.updateInput.CollectionID)
	}
}

func TestGetAssetMapsCrossWorkspaceAbsenceToNotFound(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsRead},
	}}
	assetService := &fakeAssetService{getErr: asset.ErrNotFound}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, AssetService: assetService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+httpOtherAssetID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestListAssetsPassesSearchAndPagination(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsRead},
	}}
	nextCursor := "opaque-cursor"
	auditService := &fakeAuditService{}
	assetService := &fakeAssetService{listed: asset.ListResult{
		Items: []asset.Asset{{
			ID: httpAssetID, WorkspaceID: "workspace-1", Title: "Recording",
			Search: &asset.SearchMatch{
				Title: true, ProviderIDs: []string{"mock_asr"}, Segments: []asset.SegmentHit{{
					TranscriptID: "70000000-0000-4000-8000-000000000081",
					RevisionID:   "71000000-0000-4000-8000-000000000081",
					SegmentID:    "72000000-0000-4000-8000-000000000081",
					StartMS:      1000, EndMS: 2200, Text: "Quarterly result",
				}},
			},
		}},
		NextCursor: &nextCursor,
	}}
	handler := NewApplicationHandler(Options{AuthService: authService, AssetService: assetService, AuditService: auditService})
	const (
		collectionID = "50000000-0000-4000-8000-000000000081"
		tagID        = "60000000-0000-4000-8000-000000000081"
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/assets?q=Recording&collection_id="+collectionID+
		"&tag_id="+tagID+"&status=ready&provider_id=mock_asr&speaker=Speaker%201&created_from=2026-07-01T00:00:00Z"+
		"&created_before=2026-08-01T00:00:00Z&limit=25&cursor=previous", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if assetService.listInput != (asset.ListInput{
		Query: "Recording", CollectionID: collectionID, TagID: tagID, Status: "ready",
		ProviderID: "mock_asr", Speaker: "Speaker 1",
		CreatedFrom: "2026-07-01T00:00:00Z", CreatedBefore: "2026-08-01T00:00:00Z",
		Limit: 25, Cursor: "previous",
	}) {
		t.Fatalf("List input = %+v", assetService.listInput)
	}
	var response asset.ListResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || len(response.Items) != 1 ||
		response.NextCursor == nil || response.Items[0].Search == nil || len(response.Items[0].Search.Segments) != 1 {
		t.Fatalf("decode list = (%+v, %v)", response, err)
	}
	if auditService.calls != 1 || auditService.input.Action != "asset.listed" || auditService.input.TargetType != "asset_collection" {
		t.Fatalf("audit input = %+v, calls = %d", auditService.input, auditService.calls)
	}
	if auditService.input.Metadata["provider_filter"] != true || auditService.input.Metadata["speaker_filter"] != true {
		t.Fatalf("audit metadata = %+v", auditService.input.Metadata)
	}
}

func TestListAssetsFailsClosedWhenAuditCannotBePersisted(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID:      "20000000-0000-4000-8000-000000000001",
		WorkspaceID: "10000000-0000-4000-8000-000000000001", Scopes: []string{auth.ScopeAssetsRead},
	}}
	handler := NewApplicationHandler(Options{
		AuthService: authService, AssetService: &fakeAssetService{listed: asset.ListResult{Items: []asset.Asset{}}},
		AuditService: &fakeAuditService{err: errors.New("database unavailable")},
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/assets", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), `"code":"audit_unavailable"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestListAssetsRejectsInvalidOrRepeatedLimit(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsRead},
	}}
	for _, target := range []string{
		"/api/v1/assets?limit=nope",
		"/api/v1/assets?limit=1&limit=2",
		"/api/v1/assets?status=ready&status=failed",
		"/api/v1/assets?provider_id=mock_asr&provider_id=tencent_asr",
		"/api/v1/assets?speaker=Speaker%201&speaker=Speaker%202",
	} {
		assetService := &fakeAssetService{}
		handler := NewApplicationHandler(Options{AuthService: authService, AssetService: assetService})
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want %d", target, recorder.Code, http.StatusBadRequest)
		}
		if assetService.listCalls != 0 {
			t.Fatalf("%s service calls = %d, want 0", target, assetService.listCalls)
		}
	}
}

func TestAssetLifecycleRequiresVersionAndReturnsUpdatedResource(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
	}}
	assetService := &fakeAssetService{
		trashed:  asset.Asset{ID: httpAssetID, Status: "trashed", Version: 4},
		restored: asset.Asset{ID: httpAssetID, Status: "ready", Version: 5},
	}
	handler := NewApplicationHandler(Options{AuthService: authService, AssetService: assetService})

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/assets/"+httpAssetID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("If-Match", `"3"`)
	request.Header.Set("X-Request-ID", "asset-trash")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"4"` ||
		assetService.trashAssetID != httpAssetID || assetService.trashVersion != 3 ||
		assetService.trashRequestID != "asset-trash" {
		t.Fatalf("trash response/call = %d %v / %+v: %s", recorder.Code, recorder.Header(), assetService, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/assets/"+httpAssetID+"/restore", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("If-Match", `"4"`)
	request.Header.Set("X-Request-ID", "asset-restore")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Header().Get("ETag") != `"5"` ||
		assetService.restoreAssetID != httpAssetID || assetService.restoreVersion != 4 ||
		assetService.restoreRequestID != "asset-restore" {
		t.Fatalf("restore response/call = %d %v / %+v: %s", recorder.Code, recorder.Header(), assetService, recorder.Body.String())
	}
}

func TestAssetLifecycleMapsPreconditionsAndConflict(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
	}}
	assetService := &fakeAssetService{trashErr: asset.ErrConflict}
	handler := NewApplicationHandler(Options{AuthService: authService, AssetService: assetService})

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/assets/"+httpAssetID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired || assetService.trashCalls != 0 {
		t.Fatalf("missing precondition response/calls = %d/%d", recorder.Code, assetService.trashCalls)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/v1/assets/"+httpAssetID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("If-Match", `"2"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"conflict"`) {
		t.Fatalf("conflict response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRequestAssetPurgeRequiresExplicitConfirmationAndReturnsJob(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Role: "owner",
		Scopes: []string{auth.ScopeAssetsWrite},
	}}
	assetService := &fakeAssetService{purgeRequested: asset.PurgeRequest{
		JobID: "50000000-0000-4000-8000-000000000001", AssetID: httpAssetID,
		State: "queued", RequestedAt: time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC),
	}}
	handler := NewApplicationHandler(Options{AuthService: authService, AssetService: assetService})
	newPurgeRequest := func() *http.Request {
		request := httptest.NewRequest(
			http.MethodPost, "/api/v1/assets/"+httpAssetID+"/purge",
			strings.NewReader(`{"confirmation":"`+httpAssetID+`"}`),
		)
		request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("If-Match", `"4"`)
		request.Header.Set("Idempotency-Key", "purge-asset-1")
		request.Header.Set("X-Request-ID", "asset-purge")
		return request
	}
	request := newPurgeRequest()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted ||
		recorder.Header().Get("Location") != "/api/v1/asset-purge-jobs/50000000-0000-4000-8000-000000000001" ||
		assetService.purgeCalls != 1 || assetService.purgeAssetID != httpAssetID ||
		assetService.purgeVersion != 4 || assetService.purgeInput.Confirmation != httpAssetID ||
		assetService.purgeKey != "purge-asset-1" || assetService.purgeRequestID != "asset-purge" {
		t.Fatalf("purge response/call = %d %v / %+v: %s", recorder.Code, recorder.Header(), assetService, recorder.Body.String())
	}

	assetService.purgeReplayed = true
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, newPurgeRequest())
	if recorder.Code != http.StatusOK || recorder.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replayed purge response = %d %v: %s", recorder.Code, recorder.Header(), recorder.Body.String())
	}
}

func TestGetAssetPurgeJobReturnsOwnerScopedStatus(t *testing.T) {
	jobID := "50000000-0000-4000-8000-000000000004"
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Role: "owner",
		Scopes: []string{auth.ScopeAssetsWrite},
	}}
	assetService := &fakeAssetService{purgeGot: asset.PurgeRequest{
		JobID: jobID, AssetID: httpAssetID, AssetVersion: 5, State: "running",
	}}
	handler := NewApplicationHandler(Options{AuthService: authService, AssetService: assetService})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/asset-purge-jobs/"+jobID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || assetService.getPurgeJobID != jobID ||
		!strings.Contains(recorder.Body.String(), `"state":"running"`) {
		t.Fatalf("purge status response = %d / %+v: %s", recorder.Code, assetService, recorder.Body.String())
	}
}

func TestRequestAssetPurgeMapsPreconditionBodyAndEligibilityErrors(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Role: "owner",
		Scopes: []string{auth.ScopeAssetsWrite},
	}}
	assetService := &fakeAssetService{}
	handler := NewApplicationHandler(Options{AuthService: authService, AssetService: assetService})
	newRequest := func(body string) *http.Request {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/assets/"+httpAssetID+"/purge", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "purge-key")
		return request
	}
	request := newRequest(`{"confirmation":"` + httpAssetID + `"}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired || assetService.purgeCalls != 0 {
		t.Fatalf("missing purge precondition = %d/%d", recorder.Code, assetService.purgeCalls)
	}
	request = newRequest(`{"confirmation":`)
	request.Header.Set("If-Match", `"3"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || assetService.purgeCalls != 0 {
		t.Fatalf("invalid purge body = %d/%d", recorder.Code, assetService.purgeCalls)
	}
	assetService.purgeErr = asset.ErrPurgeNotEligible
	request = newRequest(`{"confirmation":"` + httpAssetID + `"}`)
	request.Header.Set("If-Match", `"3"`)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"purge_not_eligible"`) {
		t.Fatalf("purge eligibility response = %d %s", recorder.Code, recorder.Body.String())
	}
}

type fakeAssetService struct {
	created          asset.Asset
	replayed         bool
	createErr        error
	input            asset.CreateInput
	idempotencyKey   string
	got              asset.Asset
	getErr           error
	getID            string
	listed           asset.ListResult
	listErr          error
	listInput        asset.ListInput
	listCalls        int
	updated          asset.Asset
	updateErr        error
	updateAssetID    string
	expectedVersion  int64
	updateInput      asset.UpdateMetadataInput
	updateRequestID  string
	updateCalls      int
	trashed          asset.Asset
	trashErr         error
	trashAssetID     string
	trashVersion     int64
	trashRequestID   string
	trashCalls       int
	restored         asset.Asset
	restoreErr       error
	restoreAssetID   string
	restoreVersion   int64
	restoreRequestID string
	restoreCalls     int
	purgeRequested   asset.PurgeRequest
	purgeReplayed    bool
	purgeErr         error
	purgeAssetID     string
	purgeVersion     int64
	purgeInput       asset.PurgeInput
	purgeKey         string
	purgeRequestID   string
	purgeCalls       int
	purgeGot         asset.PurgeRequest
	getPurgeErr      error
	getPurgeJobID    string
}

func (f *fakeAssetService) Create(
	_ context.Context,
	_ auth.Principal,
	input asset.CreateInput,
	idempotencyKey string,
) (asset.Asset, bool, error) {
	f.input = input
	f.idempotencyKey = idempotencyKey
	return f.created, f.replayed, f.createErr
}

func (f *fakeAssetService) Get(_ context.Context, _ auth.Principal, assetID string) (asset.Asset, error) {
	f.getID = assetID
	return f.got, f.getErr
}

func (f *fakeAssetService) List(_ context.Context, _ auth.Principal, input asset.ListInput) (asset.ListResult, error) {
	f.listCalls++
	f.listInput = input
	return f.listed, f.listErr
}

func (f *fakeAssetService) UpdateMetadata(
	_ context.Context,
	_ auth.Principal,
	assetID string,
	expectedVersion int64,
	input asset.UpdateMetadataInput,
	requestID string,
) (asset.Asset, error) {
	f.updateCalls++
	f.updateAssetID = assetID
	f.expectedVersion = expectedVersion
	f.updateInput = input
	f.updateRequestID = requestID
	return f.updated, f.updateErr
}

func (f *fakeAssetService) Trash(
	_ context.Context,
	_ auth.Principal,
	assetID string,
	expectedVersion int64,
	requestID string,
) (asset.Asset, error) {
	f.trashCalls++
	f.trashAssetID = assetID
	f.trashVersion = expectedVersion
	f.trashRequestID = requestID
	return f.trashed, f.trashErr
}

func (f *fakeAssetService) Restore(
	_ context.Context,
	_ auth.Principal,
	assetID string,
	expectedVersion int64,
	requestID string,
) (asset.Asset, error) {
	f.restoreCalls++
	f.restoreAssetID = assetID
	f.restoreVersion = expectedVersion
	f.restoreRequestID = requestID
	return f.restored, f.restoreErr
}

func (f *fakeAssetService) RequestPurge(
	_ context.Context,
	_ auth.Principal,
	assetID string,
	expectedVersion int64,
	input asset.PurgeInput,
	idempotencyKey,
	requestID string,
) (asset.PurgeRequest, bool, error) {
	f.purgeCalls++
	f.purgeAssetID = assetID
	f.purgeVersion = expectedVersion
	f.purgeInput = input
	f.purgeKey = idempotencyKey
	f.purgeRequestID = requestID
	return f.purgeRequested, f.purgeReplayed, f.purgeErr
}

func (f *fakeAssetService) GetPurge(
	_ context.Context,
	_ auth.Principal,
	jobID string,
) (asset.PurgeRequest, error) {
	f.getPurgeJobID = jobID
	return f.purgeGot, f.getPurgeErr
}

var _ AssetService = (*fakeAssetService)(nil)
