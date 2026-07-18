package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

func TestListAssetTranscriptsAuthenticatesAndReturnsItems(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptsRead},
	}}
	transcriptService := &fakeTranscriptService{summaries: []transcript.Summary{{
		ID: "transcript-1", AssetID: "asset-1", Language: "en-US",
		LatestRevisionID: "revision-1", LatestKind: transcript.KindRawASR,
		LatestText: "Welcome to VoiceAsset.",
	}}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, TranscriptService: transcriptService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+httpAssetID+"/transcripts", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if transcriptService.assetID != httpAssetID {
		t.Fatalf("List() asset ID = %q", transcriptService.assetID)
	}
	var response struct {
		Items []transcript.Summary `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil ||
		len(response.Items) != 1 || response.Items[0].LatestRevisionID != "revision-1" {
		t.Fatalf("decode response = (%+v, %v)", response, err)
	}
}

func TestGetTranscriptRevisionReturnsTimeline(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptsRead},
	}}
	transcriptService := &fakeTranscriptService{revision: transcript.Revision{
		ID: "revision-1", TranscriptID: "transcript-1", AssetID: "asset-1",
		Kind: transcript.KindRawASR, Text: "Welcome", Segments: []transcript.Segment{{
			ID: "segment-1", Ordinal: 0, StartMS: 0, EndMS: 900, Text: "Welcome",
		}},
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, TranscriptService: transcriptService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/transcript-revisions/"+httpRevisionID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if transcriptService.revisionID != httpRevisionID {
		t.Fatalf("GetRevision() ID = %q", transcriptService.revisionID)
	}
	var response transcript.Revision
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil ||
		response.ID != "revision-1" || len(response.Segments) != 1 {
		t.Fatalf("decode response = (%+v, %v)", response, err)
	}
}

func TestTranscriptRoutesHideCrossWorkspaceResources(t *testing.T) {
	authService := &fakeAuthService{principal: auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeTranscriptsRead},
	}}
	transcriptService := &fakeTranscriptService{revisionErr: transcript.ErrNotFound}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, TranscriptService: transcriptService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/transcript-revisions/"+httpRevisionID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

type fakeTranscriptService struct {
	summaries   []transcript.Summary
	listErr     error
	assetID     string
	revision    transcript.Revision
	revisionErr error
	revisionID  string
}

func (f *fakeTranscriptService) List(
	_ context.Context,
	_ auth.Principal,
	assetID string,
) ([]transcript.Summary, error) {
	f.assetID = assetID
	return f.summaries, f.listErr
}

func (f *fakeTranscriptService) GetRevision(
	_ context.Context,
	_ auth.Principal,
	revisionID string,
) (transcript.Revision, error) {
	f.revisionID = revisionID
	return f.revision, f.revisionErr
}

var _ TranscriptService = (*fakeTranscriptService)(nil)
