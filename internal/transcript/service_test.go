package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestServiceListRequiresTranscriptReadScopeAndScopesRepository(t *testing.T) {
	createdAt := time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)
	repository := &fakeRepository{summaries: []Summary{{
		ID: "transcript-1", AssetID: "asset-1", Language: "en-US",
		LatestRevisionID: "revision-1", LatestKind: KindRawASR,
		LatestText: "Welcome to VoiceAsset.", CreatedAt: createdAt,
	}}}
	service := NewService(repository)
	principal := auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1",
		Scopes: []string{auth.ScopeTranscriptsRead},
	}

	got, err := service.List(context.Background(), principal, " asset-1 ")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(got, repository.summaries) {
		t.Fatalf("List() = %+v, want %+v", got, repository.summaries)
	}
	if repository.listWorkspaceID != principal.WorkspaceID || repository.listAssetID != "asset-1" {
		t.Fatalf("repository scope = %q/%q", repository.listWorkspaceID, repository.listAssetID)
	}

	withoutScope := principal
	withoutScope.Scopes = []string{auth.ScopeAssetsRead}
	if _, err := service.List(context.Background(), withoutScope, "asset-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("List() without scope error = %v, want ErrForbidden", err)
	}
	if _, err := service.List(context.Background(), principal, " "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("List() empty asset error = %v, want ErrNotFound", err)
	}
}

func TestServiceGetRevisionReturnsImmutableTimeline(t *testing.T) {
	confidence := 0.98
	repository := &fakeRepository{revision: Revision{
		ID: "revision-1", TranscriptID: "transcript-1", AssetID: "asset-1",
		Kind: KindRawASR, Language: "zh-CN", Text: "欢迎使用语音资产。",
		ProviderSnapshot:    json.RawMessage(`{"provider_id":"mock_asr","version":"1"}`),
		HotwordSnapshot:     json.RawMessage(`{"sets":[]}`),
		ProviderRawObjectID: "object-1", SourceJobID: "job-1",
		Segments: []Segment{{
			ID: "segment-1", Ordinal: 0, StartMS: 0, EndMS: 1200,
			Text: "欢迎使用", Confidence: &confidence,
			Words: json.RawMessage(`[{"text":"欢迎","start_ms":0,"end_ms":500}]`),
		}},
	}}
	service := NewService(repository)
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptsRead}}

	got, err := service.GetRevision(context.Background(), principal, " revision-1 ")
	if err != nil {
		t.Fatalf("GetRevision() error = %v", err)
	}
	if !reflect.DeepEqual(got, repository.revision) {
		t.Fatalf("GetRevision() = %+v, want %+v", got, repository.revision)
	}
	if repository.revisionWorkspaceID != principal.WorkspaceID || repository.revisionID != "revision-1" {
		t.Fatalf("repository scope = %q/%q", repository.revisionWorkspaceID, repository.revisionID)
	}
}

func TestServiceHidesCrossWorkspaceAndRepositoryErrors(t *testing.T) {
	principal := auth.Principal{WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptsRead}}
	tests := []struct {
		name       string
		repository *fakeRepository
		operation  func(*Service) error
		want       error
	}{
		{
			name: "list not found", repository: &fakeRepository{listErr: ErrNotFound},
			operation: func(service *Service) error {
				_, err := service.List(context.Background(), principal, "asset-2")
				return err
			}, want: ErrNotFound,
		},
		{
			name: "revision not found", repository: &fakeRepository{revisionErr: ErrNotFound},
			operation: func(service *Service) error {
				_, err := service.GetRevision(context.Background(), principal, "revision-2")
				return err
			}, want: ErrNotFound,
		},
		{
			name: "storage failure", repository: &fakeRepository{revisionErr: errors.New("database unavailable")},
			operation: func(service *Service) error {
				_, err := service.GetRevision(context.Background(), principal, "revision-2")
				return err
			}, want: ErrRepository,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.operation(NewService(test.repository)); !errors.Is(err, test.want) {
				t.Fatalf("operation error = %v, want %v", err, test.want)
			}
		})
	}
}

type fakeRepository struct {
	summaries           []Summary
	revision            Revision
	listErr             error
	revisionErr         error
	listWorkspaceID     string
	listAssetID         string
	revisionWorkspaceID string
	revisionID          string
}

func (r *fakeRepository) List(_ context.Context, workspaceID, assetID string) ([]Summary, error) {
	r.listWorkspaceID = workspaceID
	r.listAssetID = assetID
	return r.summaries, r.listErr
}

func (r *fakeRepository) GetRevision(_ context.Context, workspaceID, revisionID string) (Revision, error) {
	r.revisionWorkspaceID = workspaceID
	r.revisionID = revisionID
	return r.revision, r.revisionErr
}
