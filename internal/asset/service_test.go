package asset

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestCreateNormalizesInputAndPassesIdempotencyDigest(t *testing.T) {
	repository := &fakeRepository{created: Asset{
		ID: "asset-1", WorkspaceID: "workspace-1", Title: "First recording", Language: "zh-CN", Status: "draft", Version: 1,
	}}
	service := NewService(repository)
	service.random = bytes.NewReader(append(
		bytes.Repeat([]byte{0x01}, 16),
		bytes.Repeat([]byte{0x02}, 16)...,
	))
	principal := auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
	}

	created, replayed, err := service.Create(context.Background(), principal, CreateInput{
		Title: " First recording ", Language: "zh-CN",
	}, "create-asset-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if replayed {
		t.Fatal("replayed = true, want false")
	}
	if created.ID != "asset-1" {
		t.Fatalf("created = %+v", created)
	}
	if repository.params.Title != "First recording" || repository.params.Language != "zh-CN" {
		t.Fatalf("repository params = %+v", repository.params)
	}
	if repository.params.WorkspaceID != principal.WorkspaceID || repository.params.CreatedBy != principal.UserID {
		t.Fatalf("repository ownership = %+v", repository.params)
	}
	if len(repository.params.RequestHash) != 64 || repository.params.IdempotencyKey != "create-asset-1" {
		t.Fatalf("repository idempotency = %+v", repository.params)
	}
	if repository.params.AssetID == "" || repository.params.AuditID == "" || repository.params.AssetID == repository.params.AuditID {
		t.Fatalf("generated IDs = %q/%q", repository.params.AssetID, repository.params.AuditID)
	}
}

func TestCreateRequiresAssetWriteScope(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	principal := auth.Principal{UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsRead}}

	_, _, err := service.Create(context.Background(), principal, CreateInput{Title: "Recording", Language: "en"}, "key")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Create() error = %v, want ErrForbidden", err)
	}
	if repository.createCalls != 0 {
		t.Fatalf("repository calls = %d, want 0", repository.createCalls)
	}
}

func TestCreateRejectsInvalidInputBeforeWrite(t *testing.T) {
	tests := []struct {
		name  string
		input CreateInput
		key   string
	}{
		{name: "empty title", input: CreateInput{Language: "en"}, key: "key"},
		{name: "invalid language", input: CreateInput{Title: "Recording", Language: "english"}, key: "key"},
		{name: "missing idempotency key", input: CreateInput{Title: "Recording", Language: "en"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := NewService(repository)
			principal := auth.Principal{
				UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
			}
			if _, _, err := service.Create(context.Background(), principal, test.input, test.key); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Create() error = %v, want ErrInvalidInput", err)
			}
			if repository.createCalls != 0 {
				t.Fatalf("repository calls = %d, want 0", repository.createCalls)
			}
		})
	}
}

func TestGetUsesPrincipalWorkspace(t *testing.T) {
	repository := &fakeRepository{got: Asset{ID: "asset-1", WorkspaceID: "workspace-1"}}
	service := NewService(repository)
	principal := auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsRead},
	}

	if _, err := service.Get(context.Background(), principal, "asset-1"); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if repository.getWorkspaceID != "workspace-1" || repository.getAssetID != "asset-1" {
		t.Fatalf("Get repository args = %q/%q", repository.getWorkspaceID, repository.getAssetID)
	}
}

func TestListNormalizesQueryAndReturnsOpaqueCursor(t *testing.T) {
	createdAt := time.Date(2026, time.July, 16, 12, 0, 0, 123000000, time.UTC)
	createdFrom := createdAt.Add(-24 * time.Hour)
	createdBefore := createdAt.Add(24 * time.Hour)
	const (
		collectionID = "10000000-0000-4000-8000-000000000071"
		tagID        = "20000000-0000-4000-8000-000000000071"
	)
	repository := &fakeRepository{listed: []Asset{
		{ID: "30000000-0000-4000-8000-000000000003", CreatedAt: createdAt},
		{ID: "30000000-0000-4000-8000-000000000002", CreatedAt: createdAt.Add(-time.Minute)},
	}}
	service := NewService(repository)
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsRead}}

	input := ListInput{
		Query: " Recording ", CollectionID: collectionID, TagID: tagID, Status: "ready",
		ProviderID: " mock_asr ", Speaker: " Speaker 1 ",
		CreatedFrom: createdFrom.Format(time.RFC3339Nano), CreatedBefore: createdBefore.Format(time.RFC3339Nano), Limit: 1,
	}
	result, err := service.List(context.Background(), principal, input)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != repository.listed[0].ID || result.NextCursor == nil {
		t.Fatalf("List() = %+v, want one item and cursor", result)
	}
	if repository.listParams.WorkspaceID != principal.WorkspaceID || repository.listParams.Query != "Recording" || repository.listParams.Limit != 2 {
		t.Fatalf("List repository params = %+v", repository.listParams)
	}
	if repository.listParams.CollectionID != collectionID || repository.listParams.TagID != tagID ||
		repository.listParams.Status != "ready" || repository.listParams.ProviderID != "mock_asr" ||
		repository.listParams.Speaker != "Speaker 1" || repository.listParams.CreatedFrom == nil ||
		!repository.listParams.CreatedFrom.Equal(createdFrom) || repository.listParams.CreatedBefore == nil ||
		!repository.listParams.CreatedBefore.Equal(createdBefore) {
		t.Fatalf("List repository filters = %+v", repository.listParams)
	}

	repository.listed = nil
	input.Query = "Recording"
	input.Cursor = *result.NextCursor
	if _, err := service.List(context.Background(), principal, input); err != nil {
		t.Fatalf("List(cursor) error = %v", err)
	}
	if repository.listParams.BeforeCreatedAt == nil || !repository.listParams.BeforeCreatedAt.Equal(createdAt) {
		t.Fatalf("cursor time = %v, want %v", repository.listParams.BeforeCreatedAt, createdAt)
	}
	if repository.listParams.BeforeID != "30000000-0000-4000-8000-000000000003" {
		t.Fatalf("cursor ID = %q", repository.listParams.BeforeID)
	}
	input.Speaker = "Speaker 2"
	if _, err := service.List(context.Background(), principal, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("List(changed speaker) error = %v, want ErrInvalidInput", err)
	}
}

func TestListValidatesScopeLimitQueryAndCursor(t *testing.T) {
	tests := []struct {
		name      string
		principal auth.Principal
		input     ListInput
		want      error
	}{
		{name: "scope", principal: auth.Principal{}, want: ErrForbidden},
		{name: "large limit", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{Limit: 101}, want: ErrInvalidInput},
		{name: "control query", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{Query: "bad\nquery"}, want: ErrInvalidInput},
		{name: "collection", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{CollectionID: "bad"}, want: ErrInvalidInput},
		{name: "tag", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{TagID: "bad"}, want: ErrInvalidInput},
		{name: "status", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{Status: "deleted"}, want: ErrInvalidInput},
		{name: "provider", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{ProviderID: "unknown_asr"}, want: ErrInvalidInput},
		{name: "speaker control", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{Speaker: "Speaker\n1"}, want: ErrInvalidInput},
		{name: "speaker length", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{Speaker: strings.Repeat("s", 201)}, want: ErrInvalidInput},
		{name: "date", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{CreatedFrom: "yesterday"}, want: ErrInvalidInput},
		{name: "date range", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{CreatedFrom: "2026-07-17T00:00:00Z", CreatedBefore: "2026-07-16T00:00:00Z"}, want: ErrInvalidInput},
		{name: "invalid cursor", principal: auth.Principal{Scopes: []string{auth.ScopeAssetsRead}}, input: ListInput{Cursor: "not-base64!"}, want: ErrInvalidInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := NewService(repository)
			if _, err := service.List(context.Background(), test.principal, test.input); !errors.Is(err, test.want) {
				t.Fatalf("List() error = %v, want %v", err, test.want)
			}
			if repository.listCalls != 0 {
				t.Fatalf("repository calls = %d, want 0", repository.listCalls)
			}
		})
	}
}

func TestLifecycleChangesRequireAssetWriteAndPreserveAttribution(t *testing.T) {
	const (
		workspaceID  = "10000000-0000-4000-8000-000000000081"
		userID       = "20000000-0000-4000-8000-000000000081"
		credentialID = "30000000-0000-4000-8000-000000000081"
		assetID      = "40000000-0000-4000-8000-000000000081"
	)
	repository := &fakeRepository{
		trashed:  Asset{ID: assetID, Status: "trashed", Version: 4},
		restored: Asset{ID: assetID, Status: "ready", Version: 5},
	}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x08}, 48))
	principal := auth.Principal{
		UserID: userID, WorkspaceID: workspaceID, Role: "agent",
		CredentialType: "api_key", CredentialID: credentialID,
		Scopes: []string{auth.ScopeAssetsWrite},
	}

	trashed, err := service.Trash(context.Background(), principal, assetID, 3, "trash-request")
	if err != nil || trashed.Status != "trashed" || trashed.Version != 4 {
		t.Fatalf("Trash() = (%+v, %v)", trashed, err)
	}
	if params := repository.trashParams; params.AssetID != assetID || params.WorkspaceID != workspaceID ||
		params.ExpectedVersion != 3 || params.ActorType != "agent" || params.CredentialID != credentialID ||
		params.RequestID != "trash-request" || params.AuditID == "" {
		t.Fatalf("trash params = %+v", params)
	}
	restored, err := service.Restore(context.Background(), principal, assetID, 4, "restore-request")
	if err != nil || restored.Status != "ready" || restored.Version != 5 {
		t.Fatalf("Restore() = (%+v, %v)", restored, err)
	}
	if params := repository.restoreParams; params.ExpectedVersion != 4 || params.RequestID != "restore-request" || params.AuditID == "" {
		t.Fatalf("restore params = %+v", params)
	}

	if _, err := service.Trash(context.Background(), auth.Principal{}, assetID, 5, "request"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Trash(scope) error = %v", err)
	}
	if _, err := service.Restore(context.Background(), principal, "bad", 5, "request"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Restore(input) error = %v", err)
	}
	repository.trashErr = ErrConflict
	if _, err := service.Trash(context.Background(), principal, assetID, 5, "conflict-request"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Trash(conflict) error = %v", err)
	}
}

func TestRequestPurgeRequiresOwnerConfirmationAndCreatesDurableJob(t *testing.T) {
	const (
		workspaceID = "10000000-0000-4000-8000-000000000091"
		userID      = "20000000-0000-4000-8000-000000000091"
		assetID     = "40000000-0000-4000-8000-000000000091"
	)
	requestedAt := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	repository := &fakeRepository{purgeRequested: PurgeRequest{
		JobID: "50000000-0000-4000-8000-000000000091", AssetID: assetID,
		State: "queued", RequestedAt: requestedAt,
	}}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x09}, 32))
	principal := auth.Principal{
		UserID: userID, WorkspaceID: workspaceID, Role: "owner",
		Scopes: []string{auth.ScopeAssetsWrite},
	}

	requested, replayed, err := service.RequestPurge(
		context.Background(), principal, assetID, 4,
		PurgeInput{Confirmation: assetID}, "purge-asset-1", "purge-request",
	)
	if err != nil || replayed || requested.JobID == "" || requested.State != "queued" {
		t.Fatalf("RequestPurge() = (%+v, %v, %v)", requested, replayed, err)
	}
	params := repository.purgeParams
	if params.AssetID != assetID || params.WorkspaceID != workspaceID || params.ActorID != userID ||
		params.ActorType != "user" || params.ExpectedVersion != 4 ||
		params.IdempotencyKey != "purge-asset-1" || len(params.RequestHash) != 64 ||
		params.RequestID != "purge-request" || params.JobID == "" || params.AuditID == "" {
		t.Fatalf("purge params = %+v", params)
	}
}

func TestRequestPurgeRejectsUnsafeInputAndMapsRepositoryErrors(t *testing.T) {
	const assetID = "40000000-0000-4000-8000-00000000009a"
	owner := auth.Principal{
		UserID:      "20000000-0000-4000-8000-000000000092",
		WorkspaceID: "10000000-0000-4000-8000-000000000092",
		Role:        "owner", Scopes: []string{auth.ScopeAssetsWrite},
	}
	tests := []struct {
		name      string
		principal auth.Principal
		assetID   string
		version   int64
		input     PurgeInput
		key       string
		requestID string
		repoErr   error
		want      error
		calls     int
	}{
		{name: "non-owner", principal: auth.Principal{Role: "admin", Scopes: []string{auth.ScopeAssetsWrite}}, assetID: assetID, version: 2, input: PurgeInput{Confirmation: assetID}, key: "key", requestID: "request", want: ErrForbidden},
		{name: "confirmation", principal: owner, assetID: assetID, version: 2, input: PurgeInput{Confirmation: "40000000-0000-4000-8000-000000000099"}, key: "key", requestID: "request", want: ErrInvalidInput},
		{name: "non-canonical confirmation", principal: owner, assetID: assetID, version: 2, input: PurgeInput{Confirmation: strings.ToUpper(assetID)}, key: "key", requestID: "request", want: ErrInvalidInput},
		{name: "idempotency", principal: owner, assetID: assetID, version: 2, input: PurgeInput{Confirmation: assetID}, requestID: "request", want: ErrInvalidInput},
		{name: "not eligible", principal: owner, assetID: assetID, version: 2, input: PurgeInput{Confirmation: assetID}, key: "key", requestID: "request", repoErr: ErrPurgeNotEligible, want: ErrPurgeNotEligible, calls: 1},
		{name: "conflict", principal: owner, assetID: assetID, version: 2, input: PurgeInput{Confirmation: assetID}, key: "key", requestID: "request", repoErr: ErrConflict, want: ErrConflict, calls: 1},
		{name: "idempotency conflict", principal: owner, assetID: assetID, version: 2, input: PurgeInput{Confirmation: assetID}, key: "key", requestID: "request", repoErr: ErrIdempotencyConflict, want: ErrIdempotencyConflict, calls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{purgeErr: test.repoErr}
			service := NewService(repository)
			service.random = bytes.NewReader(bytes.Repeat([]byte{0x0a}, 32))
			_, _, err := service.RequestPurge(
				context.Background(), test.principal, test.assetID, test.version,
				test.input, test.key, test.requestID,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("RequestPurge() error = %v, want %v", err, test.want)
			}
			if repository.purgeCalls != test.calls {
				t.Fatalf("repository calls = %d, want %d", repository.purgeCalls, test.calls)
			}
		})
	}
}

func TestGetPurgeIsOwnerScopedAndMapsNotFound(t *testing.T) {
	const jobID = "50000000-0000-4000-8000-000000000093"
	repository := &fakeRepository{purgeGot: PurgeRequest{JobID: jobID, State: "running"}}
	service := NewService(repository)
	owner := auth.Principal{
		WorkspaceID: "10000000-0000-4000-8000-000000000093", Role: "owner",
		Scopes: []string{auth.ScopeAssetsWrite},
	}
	result, err := service.GetPurge(context.Background(), owner, jobID)
	if err != nil || result.JobID != jobID || repository.getPurgeWorkspaceID != owner.WorkspaceID {
		t.Fatalf("GetPurge() = (%+v, %v), repository = %+v", result, err, repository)
	}
	if _, err := service.GetPurge(context.Background(), auth.Principal{Role: "admin", Scopes: []string{auth.ScopeAssetsWrite}}, jobID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetPurge(non-owner) error = %v", err)
	}
	repository.getPurgeErr = ErrNotFound
	if _, err := service.GetPurge(context.Background(), owner, jobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPurge(not found) error = %v", err)
	}
}

func TestUpdateMetadataNormalizesFullReplacementAndPreservesAgentAttribution(t *testing.T) {
	const (
		workspaceID  = "10000000-0000-4000-8000-000000000041"
		userID       = "20000000-0000-4000-8000-000000000041"
		credentialID = "30000000-0000-4000-8000-000000000041"
		assetID      = "40000000-0000-4000-8000-000000000041"
		collectionID = "50000000-0000-4000-8000-000000000041"
	)
	repository := &fakeRepository{updated: Asset{
		ID: assetID, WorkspaceID: workspaceID, Title: "Planning call", Language: "zh-CN", Version: 4,
	}}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x03}, 16))
	principal := auth.Principal{
		UserID: userID, WorkspaceID: workspaceID, Role: "agent",
		CredentialType: "api_key", CredentialID: credentialID,
		Scopes: []string{auth.ScopeMetadataWrite},
	}

	updated, err := service.UpdateMetadata(context.Background(), principal, assetID, 3, UpdateMetadataInput{
		Title: " Planning call ", Language: "zh-cn", CollectionID: stringPointer(collectionID),
	}, "asset-metadata-update")
	if err != nil || updated.Version != 4 {
		t.Fatalf("UpdateMetadata() = (%+v, %v)", updated, err)
	}
	params := repository.updateParams
	if params.AssetID != assetID || params.WorkspaceID != workspaceID || params.Title != "Planning call" ||
		params.Language != "zh-CN" || params.CollectionID == nil || *params.CollectionID != collectionID ||
		params.ExpectedVersion != 3 || params.RequestID != "asset-metadata-update" {
		t.Fatalf("update params = %+v", params)
	}
	if params.ActorType != "agent" || params.ActorID != userID || params.CredentialID != credentialID || params.AuditID == "" {
		t.Fatalf("update attribution = %+v", params)
	}
}

func TestUpdateMetadataRejectsInvalidInputScopeAndMapsRepositoryErrors(t *testing.T) {
	const assetID = "40000000-0000-4000-8000-000000000051"
	valid := UpdateMetadataInput{Title: "Planning call", Language: "en", CollectionID: nil}
	tests := []struct {
		name      string
		principal auth.Principal
		assetID   string
		version   int64
		input     UpdateMetadataInput
		requestID string
		repoErr   error
		want      error
		wantCalls int
	}{
		{name: "scope", principal: auth.Principal{}, assetID: assetID, version: 1, input: valid, requestID: "request", want: ErrForbidden},
		{name: "asset ID", principal: metadataWritePrincipal(), assetID: "bad", version: 1, input: valid, requestID: "request", want: ErrInvalidInput},
		{name: "version", principal: metadataWritePrincipal(), assetID: assetID, input: valid, requestID: "request", want: ErrInvalidInput},
		{name: "collection", principal: metadataWritePrincipal(), assetID: assetID, version: 1, input: UpdateMetadataInput{Title: "Title", Language: "en", CollectionID: stringPointer("bad")}, requestID: "request", want: ErrInvalidInput},
		{name: "request ID", principal: metadataWritePrincipal(), assetID: assetID, version: 1, input: valid, requestID: "bad\nrequest", want: ErrInvalidInput},
		{name: "not found", principal: metadataWritePrincipal(), assetID: assetID, version: 1, input: valid, requestID: "request", repoErr: ErrNotFound, want: ErrNotFound, wantCalls: 1},
		{name: "conflict", principal: metadataWritePrincipal(), assetID: assetID, version: 1, input: valid, requestID: "request", repoErr: ErrConflict, want: ErrConflict, wantCalls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{updateErr: test.repoErr}
			service := NewService(repository)
			if _, err := service.UpdateMetadata(
				context.Background(), test.principal, test.assetID, test.version, test.input, test.requestID,
			); !errors.Is(err, test.want) {
				t.Fatalf("UpdateMetadata() error = %v, want %v", err, test.want)
			}
			if repository.updateCalls != test.wantCalls {
				t.Fatalf("repository calls = %d, want %d", repository.updateCalls, test.wantCalls)
			}
		})
	}
}

func metadataWritePrincipal() auth.Principal {
	return auth.Principal{
		UserID:      "20000000-0000-4000-8000-000000000051",
		WorkspaceID: "10000000-0000-4000-8000-000000000051",
		Scopes:      []string{auth.ScopeMetadataWrite},
	}
}

func stringPointer(value string) *string {
	return &value
}

type fakeRepository struct {
	params              CreateParams
	created             Asset
	replayed            bool
	createErr           error
	createCalls         int
	got                 Asset
	getErr              error
	getWorkspaceID      string
	getAssetID          string
	listed              []Asset
	listErr             error
	listParams          ListParams
	listCalls           int
	updated             Asset
	updateErr           error
	updateParams        UpdateMetadataParams
	updateCalls         int
	trashed             Asset
	trashErr            error
	trashParams         LifecycleParams
	restored            Asset
	restoreErr          error
	restoreParams       LifecycleParams
	purgeRequested      PurgeRequest
	purgeReplayed       bool
	purgeErr            error
	purgeParams         PurgeParams
	purgeCalls          int
	purgeGot            PurgeRequest
	getPurgeErr         error
	getPurgeWorkspaceID string
	getPurgeJobID       string
}

func (f *fakeRepository) Create(_ context.Context, params CreateParams) (Asset, bool, error) {
	f.createCalls++
	f.params = params
	return f.created, f.replayed, f.createErr
}

func (f *fakeRepository) Get(_ context.Context, workspaceID, assetID string) (Asset, error) {
	f.getWorkspaceID = workspaceID
	f.getAssetID = assetID
	return f.got, f.getErr
}

func (f *fakeRepository) List(_ context.Context, params ListParams) ([]Asset, error) {
	f.listCalls++
	f.listParams = params
	return f.listed, f.listErr
}

func (f *fakeRepository) UpdateMetadata(_ context.Context, params UpdateMetadataParams) (Asset, error) {
	f.updateCalls++
	f.updateParams = params
	return f.updated, f.updateErr
}

func (f *fakeRepository) Trash(_ context.Context, params LifecycleParams) (Asset, error) {
	f.trashParams = params
	return f.trashed, f.trashErr
}

func (f *fakeRepository) Restore(_ context.Context, params LifecycleParams) (Asset, error) {
	f.restoreParams = params
	return f.restored, f.restoreErr
}

func (f *fakeRepository) RequestPurge(_ context.Context, params PurgeParams) (PurgeRequest, bool, error) {
	f.purgeCalls++
	f.purgeParams = params
	return f.purgeRequested, f.purgeReplayed, f.purgeErr
}

func (f *fakeRepository) GetPurge(_ context.Context, workspaceID, jobID string) (PurgeRequest, error) {
	f.getPurgeWorkspaceID = workspaceID
	f.getPurgeJobID = jobID
	return f.purgeGot, f.getPurgeErr
}
