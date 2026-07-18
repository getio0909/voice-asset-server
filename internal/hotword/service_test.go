package hotword

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/auth"
)

const (
	testWorkspaceID = "10000000-0000-4000-8000-000000000091"
	testUserID      = "20000000-0000-4000-8000-000000000091"
	testAssetID     = "30000000-0000-4000-8000-000000000091"
	testSetID       = "40000000-0000-4000-8000-000000000091"
)

func TestServiceCreatesValidatedVersionedHotwordSet(t *testing.T) {
	repository := &fakeRepository{created: Set{ID: testSetID, ResourceVersion: 1, CurrentVersion: 1}}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x41}, 64))
	principal := auth.Principal{
		UserID: testUserID, WorkspaceID: testWorkspaceID,
		Scopes: []string{auth.ScopeAdminWrite},
	}
	created, err := service.Create(context.Background(), principal, CreateInput{
		DisplayName: " Product names ", ScopeType: ScopeWorkspace,
		Entries: []EntryInput{{
			Term: " VoiceAsset ", Aliases: []string{"GetIO"}, Language: "zh-CN", Weight: 80,
			ProviderMapping: map[string]json.RawMessage{
				asr.TencentProviderID: json.RawMessage(`{"category":"product"}`),
			},
		}},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID != testSetID || repository.createParams.WorkspaceID != testWorkspaceID ||
		repository.createParams.CreatedBy != testUserID || repository.createParams.DisplayName != "Product names" ||
		repository.createParams.ScopeType != ScopeWorkspace || repository.createParams.ScopeID != nil ||
		repository.createParams.State != StateEnabled {
		t.Fatalf("Create() = %+v, params = %+v", created, repository.createParams)
	}
	if repository.createParams.SetID == "" || repository.createParams.VersionID == "" ||
		repository.createParams.AuditID == "" {
		t.Fatal("Create() did not generate stable identifiers")
	}
	entries, err := decodeEntries(repository.createParams.EntriesJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Term != "VoiceAsset" ||
		!reflect.DeepEqual(entries[0].Aliases, []string{"GetIO"}) || !entries[0].Enabled ||
		entries[0].Weight != 80 {
		t.Fatalf("normalized entries = %+v", entries)
	}
	if _, ok := entries[0].ProviderMapping[asr.TencentProviderID]; !ok {
		t.Fatalf("provider mapping = %+v", entries[0].ProviderMapping)
	}
}

func TestEntryValidationRejectsUnsafeOrAmbiguousTerms(t *testing.T) {
	falseValue := false
	tests := []struct {
		name    string
		entries []EntryInput
	}{
		{name: "empty", entries: nil},
		{name: "delimiter", entries: []EntryInput{{Term: "bad,term", Language: "zh-CN", Weight: 50}}},
		{name: "control", entries: []EntryInput{{Term: "bad\nterm", Language: "zh-CN", Weight: 50}}},
		{name: "weight", entries: []EntryInput{{Term: "valid", Language: "en-US", Weight: 101}}},
		{name: "language", entries: []EntryInput{{Term: "valid", Language: "not a locale", Weight: 50}}},
		{name: "duplicate alias", entries: []EntryInput{{Term: "VoiceAsset", Aliases: []string{"voiceasset"}, Language: "zh-CN", Weight: 50}}},
		{name: "duplicate entries", entries: []EntryInput{
			{Term: "VoiceAsset", Language: "zh-CN", Weight: 50},
			{Term: "voiceasset", Language: "zh-CN", Weight: 60},
		}},
		{name: "unknown provider", entries: []EntryInput{{
			Term: "valid", Language: "en-US", Weight: 50,
			ProviderMapping: map[string]json.RawMessage{"unknown": json.RawMessage(`{}`)},
		}}},
		{name: "primitive mapping", entries: []EntryInput{{
			Term: "valid", Language: "en-US", Weight: 50,
			ProviderMapping: map[string]json.RawMessage{asr.TencentProviderID: json.RawMessage(`"raw"`)},
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeAndEncodeEntries(test.entries); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("normalizeAndEncodeEntries() error = %v", err)
			}
		})
	}
	encoded, err := normalizeAndEncodeEntries([]EntryInput{{
		Term: "disabled", Language: "en-US", Weight: 1, Enabled: &falseValue,
	}})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := decodeEntries(encoded)
	if entries[0].Enabled {
		t.Fatal("explicit disabled value was not preserved")
	}
}

func TestServiceScopesMutationsAndUsesOptimisticResourceVersions(t *testing.T) {
	repository := &fakeRepository{
		versioned: Set{ID: testSetID, CurrentVersion: 2, ResourceVersion: 2},
		updated:   Set{ID: testSetID, State: StateDisabled, ResourceVersion: 3},
	}
	service := NewService(repository)
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 64))
	principal := auth.Principal{
		UserID: testUserID, WorkspaceID: testWorkspaceID,
		Scopes: []string{auth.ScopeAdminWrite},
	}
	versioned, err := service.AddVersion(context.Background(), principal, testSetID, 1, AddVersionInput{
		Entries: []EntryInput{{Term: "VoiceAsset", Language: "zh-CN", Weight: 90}},
	})
	if err != nil || versioned.CurrentVersion != 2 ||
		repository.addVersionParams.ExpectedResourceVersion != 1 ||
		repository.addVersionParams.WorkspaceID != testWorkspaceID {
		t.Fatalf("AddVersion() = (%+v, %v), params = %+v", versioned, err, repository.addVersionParams)
	}
	disabled := StateDisabled
	updated, err := service.Update(context.Background(), principal, testSetID, 2, UpdateInput{State: &disabled})
	if err != nil || updated.State != StateDisabled ||
		repository.updateParams.ExpectedResourceVersion != 2 || repository.updateParams.State != StateDisabled {
		t.Fatalf("Update() = (%+v, %v), params = %+v", updated, err, repository.updateParams)
	}
	if _, err := service.AddVersion(context.Background(), principal, "not-a-uuid", 1, AddVersionInput{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid ID error = %v", err)
	}
	viewer := principal
	viewer.Scopes = []string{auth.ScopeAdminRead}
	if _, err := service.Update(context.Background(), viewer, testSetID, 2, UpdateInput{State: &disabled}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer Update() error = %v", err)
	}
}

func TestResolveForAssetAppliesScopePrecedenceAndRecordsVersions(t *testing.T) {
	assetScope := testAssetID
	repository := &fakeRepository{resolved: []resolvedSet{
		{
			ID: "50000000-0000-4000-8000-000000000091", ScopeType: ScopeAsset,
			ScopeID: &assetScope, CurrentVersion: 4,
			Entries: []Entry{
				{Term: "VoiceAsset", Language: "zh-CN", Weight: 95, Enabled: true},
				{Term: "disabled", Language: "en-US", Weight: 100, Enabled: false},
			},
		},
		{
			ID: "40000000-0000-4000-8000-000000000091", ScopeType: ScopeWorkspace,
			CurrentVersion: 2,
			Entries: []Entry{{
				Term: "voiceasset", Aliases: []string{"GetIO"}, Language: "zh-CN", Weight: 30, Enabled: true,
			}},
		},
	}}
	resolution, err := NewService(repository).ResolveForAsset(
		context.Background(), testWorkspaceID, testAssetID,
	)
	if err != nil {
		t.Fatalf("ResolveForAsset() error = %v", err)
	}
	if repository.resolveWorkspaceID != testWorkspaceID || repository.resolveAssetID != testAssetID {
		t.Fatalf("repository scope = %q/%q", repository.resolveWorkspaceID, repository.resolveAssetID)
	}
	want := []asr.Hotword{{Term: "VoiceAsset", Weight: 95}, {Term: "GetIO", Weight: 30}}
	if !reflect.DeepEqual(resolution.Hotwords, want) {
		t.Fatalf("hotwords = %+v, want %+v", resolution.Hotwords, want)
	}
	var snapshot struct {
		Sets []struct {
			ID        string `json:"id"`
			Version   int    `json:"version"`
			ScopeType string `json:"scope_type"`
		} `json:"sets"`
	}
	if err := json.Unmarshal(resolution.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sets) != 2 || snapshot.Sets[0].ScopeType != ScopeWorkspace ||
		snapshot.Sets[0].Version != 2 || snapshot.Sets[1].ScopeType != ScopeAsset ||
		snapshot.Sets[1].Version != 4 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestServiceMapsRepositoryFailuresWithoutLeakingDetails(t *testing.T) {
	repository := &fakeRepository{listErr: errors.New("postgres password=secret")}
	principal := auth.Principal{WorkspaceID: testWorkspaceID, Scopes: []string{auth.ScopeAdminRead}}
	_, err := NewService(repository).List(context.Background(), principal)
	if !errors.Is(err, ErrRepository) || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("List() error = %v", err)
	}
	repository.listErr = ErrConflict
	_, err = NewService(repository).List(context.Background(), principal)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("List() conflict error = %v", err)
	}
}

type fakeRepository struct {
	createParams       CreateParams
	created            Set
	createErr          error
	listWorkspaceID    string
	listed             []Set
	listErr            error
	addVersionParams   AddVersionParams
	versioned          Set
	addVersionErr      error
	updateParams       UpdateStateParams
	updated            Set
	updateErr          error
	resolveWorkspaceID string
	resolveAssetID     string
	resolved           []resolvedSet
	resolveErr         error
}

func (repository *fakeRepository) Create(_ context.Context, params CreateParams) (Set, error) {
	repository.createParams = params
	return repository.created, repository.createErr
}

func (repository *fakeRepository) List(_ context.Context, workspaceID string) ([]Set, error) {
	repository.listWorkspaceID = workspaceID
	return repository.listed, repository.listErr
}

func (repository *fakeRepository) AddVersion(_ context.Context, params AddVersionParams) (Set, error) {
	repository.addVersionParams = params
	return repository.versioned, repository.addVersionErr
}

func (repository *fakeRepository) UpdateState(_ context.Context, params UpdateStateParams) (Set, error) {
	repository.updateParams = params
	return repository.updated, repository.updateErr
}

func (repository *fakeRepository) Resolve(
	_ context.Context,
	workspaceID,
	assetID string,
) ([]resolvedSet, error) {
	repository.resolveWorkspaceID = workspaceID
	repository.resolveAssetID = assetID
	return repository.resolved, repository.resolveErr
}

var _ Repository = (*fakeRepository)(nil)
