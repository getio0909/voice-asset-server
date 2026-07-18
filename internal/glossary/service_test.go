package glossary

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

type memoryRepository struct {
	created CreateParams
	sets    []resolvedSet
	err     error
}

func (repository *memoryRepository) Create(_ context.Context, params CreateParams) (Set, error) {
	repository.created = params
	if repository.err != nil {
		return Set{}, repository.err
	}
	entries, _ := decodeEntries(params.EntriesJSON)
	return Set{ID: params.SetID, WorkspaceID: params.WorkspaceID, Entries: entries, ResourceVersion: 1}, nil
}

func (*memoryRepository) List(context.Context, string) ([]Set, error) { return nil, nil }
func (*memoryRepository) AddVersion(context.Context, AddVersionParams) (Set, error) {
	return Set{}, nil
}
func (*memoryRepository) UpdateState(context.Context, UpdateStateParams) (Set, error) {
	return Set{}, nil
}
func (repository *memoryRepository) Resolve(context.Context, string, string, string) ([]resolvedSet, error) {
	return repository.sets, repository.err
}

func adminPrincipal() auth.Principal {
	return auth.Principal{
		UserID: "11111111-1111-4111-8111-111111111111", WorkspaceID: "22222222-2222-4222-8222-222222222222",
		Role: "owner", Scopes: auth.ScopesForRole("owner"),
	}
}

func validEntry() Entry {
	return Entry{
		CanonicalForm: "容器云", Aliases: []string{"容易云"}, Language: "zh-CN",
		ContextTerms: []string{"调度"}, ForbiddenContexts: []string{"很容易"}, Priority: 100,
		Description: "domain correction",
	}
}

func TestCreateNormalizesAndPersistsVersionedEntries(t *testing.T) {
	repository := &memoryRepository{}
	created, err := NewService(repository).Create(context.Background(), adminPrincipal(), CreateInput{
		DisplayName: " Platform terms ", ScopeType: ScopeWorkspace, Entries: []Entry{validEntry()},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ResourceVersion != 1 || repository.created.State != StateEnabled {
		t.Fatalf("created = %#v; params = %#v", created, repository.created)
	}
	var entries []Entry
	if err := json.Unmarshal(repository.created.EntriesJSON, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].CanonicalForm != "容器云" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestCreateRejectsDuplicateAliasesAndInvalidRegex(t *testing.T) {
	for name, entries := range map[string][]Entry{
		"duplicate": {
			validEntry(),
			{CanonicalForm: "Other", Aliases: []string{"容易云"}, Language: "zh-CN", Priority: 1},
		},
		"regex": {
			{CanonicalForm: "Cloud", Aliases: []string{"["}, Language: "en", Regex: true, Priority: 1},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewService(&memoryRepository{}).Create(context.Background(), adminPrincipal(), CreateInput{
				DisplayName: name, ScopeType: ScopeWorkspace, Entries: entries,
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestResolveUsesAssetOverrideAndRecordsVersions(t *testing.T) {
	workspace := validEntry()
	asset := validEntry()
	asset.CanonicalForm = "容器云平台"
	repository := &memoryRepository{sets: []resolvedSet{
		{ID: "workspace", ScopeType: ScopeWorkspace, CurrentVersion: 2, Entries: []Entry{workspace}},
		{ID: "asset", ScopeType: ScopeAsset, CurrentVersion: 4, Entries: []Entry{asset}},
	}}
	resolution, err := NewService(repository).ResolveForAsset(context.Background(), "workspace", "asset")
	if err != nil {
		t.Fatalf("ResolveForAsset() error = %v", err)
	}
	if len(resolution.Rules) != 1 || resolution.Rules[0].CanonicalForm != "容器云平台" {
		t.Fatalf("rules = %#v", resolution.Rules)
	}
	if !json.Valid(resolution.Snapshot) || string(resolution.Snapshot) == "{}" {
		t.Fatalf("snapshot = %s", resolution.Snapshot)
	}
}

func TestRepositoryErrorsAreRedacted(t *testing.T) {
	repository := &memoryRepository{err: errors.New("postgres secret detail")}
	_, err := NewService(repository).ResolveForAsset(context.Background(), "workspace", "asset")
	if !errors.Is(err, ErrRepository) || err.Error() != "glossary repository failure: resolve glossary sets" {
		t.Fatalf("error = %v", err)
	}
}
