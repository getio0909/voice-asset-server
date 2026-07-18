package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/clip"
	"github.com/getio0909/voice-asset-server/internal/transcriptexport"
)

const (
	httpClipID   = "50000000-0000-4000-8000-000000000081"
	httpExportID = "60000000-0000-4000-8000-000000000081"
)

func TestCreateAudioClipReturnsMetadataAndAuthenticatedDownloadURL(t *testing.T) {
	service := &fakeClipService{clip: clip.Clip{
		ID: httpClipID, AssetID: httpAssetID, StartMS: 500, EndMS: 2_000,
		DurationMS: 1_500, MIMEType: "audio/wav", FileSize: 48_044,
		SHA256: strings.Repeat("a", 64), DownloadURL: "/api/v1/audio-clips/" + httpClipID,
		CreatedAt: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 7, 16, 13, 0, 0, 0, time.UTC),
	}}
	handler := artifactHandler(service, nil, nil)
	request := httptest.NewRequest(
		http.MethodPost, "/api/v1/assets/"+httpAssetID+"/clips",
		strings.NewReader(`{"start_ms":500,"end_ms":2000}`),
	)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "request-create-clip")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/v1/audio-clips/"+httpClipID {
		t.Fatalf("Location = %q", recorder.Header().Get("Location"))
	}
	if service.assetID != httpAssetID || service.input != (clip.CreateInput{StartMS: 500, EndMS: 2_000}) ||
		service.requestID != "request-create-clip" {
		t.Fatalf("service call = %q/%+v/%q", service.assetID, service.input, service.requestID)
	}
	if strings.Contains(recorder.Body.String(), "base64") || strings.Contains(recorder.Body.String(), "content") {
		t.Fatalf("response unexpectedly contains embedded media: %s", recorder.Body.String())
	}
}

func TestDownloadAudioClipServesRangeAndRecordsReadAudit(t *testing.T) {
	auditService := &fakeAuditService{}
	service := &fakeClipService{media: clip.Media{
		ClipID: httpClipID, MIMEType: "audio/wav", Size: 10,
		SHA256: strings.Repeat("b", 64), Content: openHTTPAudioFixture(t, []byte("0123456789")),
	}}
	handler := artifactHandler(service, nil, auditService)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/audio-clips/"+httpClipID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("Range", "bytes=2-5")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "2345" {
		t.Fatalf("response = %d/%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Disposition") != `attachment; filename="`+httpClipID+`.wav"` ||
		recorder.Header().Get("ETag") != `"`+strings.Repeat("b", 64)+`"` {
		t.Fatalf("headers = %v", recorder.Header())
	}
	if auditService.calls != 1 || auditService.input.Action != "audio_clip.read" ||
		auditService.input.TargetType != "audio_clip" || auditService.input.TargetID != httpClipID {
		t.Fatalf("audit = %+v/%d", auditService.input, auditService.calls)
	}
}

func TestCreateAndDownloadTranscriptExport(t *testing.T) {
	exportService := &fakeTranscriptExportService{export: transcriptexport.Export{
		ID: httpExportID, AssetID: httpAssetID, RevisionID: httpRevisionID,
		Format: transcriptexport.FormatVTT, MIMEType: "text/vtt; charset=utf-8",
		FileSize: 44, SHA256: strings.Repeat("c", 64),
		DownloadURL: "/api/v1/transcript-exports/" + httpExportID,
		CreatedAt:   time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		ExpiresAt:   time.Date(2026, 7, 16, 13, 0, 0, 0, time.UTC),
	}}
	handler := artifactHandler(nil, exportService, nil)
	createRequest := httptest.NewRequest(
		http.MethodPost, "/api/v1/transcript-revisions/"+httpRevisionID+"/exports",
		strings.NewReader(`{"format":"vtt"}`),
	)
	createRequest.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("X-Request-ID", "request-create-export")
	createRecorder := httptest.NewRecorder()

	handler.ServeHTTP(createRecorder, createRequest)

	if createRecorder.Code != http.StatusCreated ||
		createRecorder.Header().Get("Location") != "/api/v1/transcript-exports/"+httpExportID {
		t.Fatalf("create response = %d/%v/%s", createRecorder.Code, createRecorder.Header(), createRecorder.Body.String())
	}
	if exportService.revisionID != httpRevisionID || exportService.input.Format != transcriptexport.FormatVTT ||
		exportService.requestID != "request-create-export" {
		t.Fatalf("service call = %q/%+v/%q", exportService.revisionID, exportService.input, exportService.requestID)
	}

	auditService := &fakeAuditService{}
	exportService.media = transcriptexport.Media{
		ExportID: httpExportID, MIMEType: "text/vtt; charset=utf-8", Extension: "vtt",
		Size: 10, SHA256: strings.Repeat("d", 64), Content: openHTTPAudioFixture(t, []byte("WEBVTTtext")),
	}
	handler = artifactHandler(nil, exportService, auditService)
	downloadRequest := httptest.NewRequest(http.MethodHead, "/api/v1/transcript-exports/"+httpExportID, nil)
	downloadRequest.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	downloadRecorder := httptest.NewRecorder()

	handler.ServeHTTP(downloadRecorder, downloadRequest)

	if downloadRecorder.Code != http.StatusOK || downloadRecorder.Body.Len() != 0 ||
		downloadRecorder.Header().Get("Content-Length") != "10" ||
		downloadRecorder.Header().Get("Content-Disposition") != `attachment; filename="`+httpExportID+`.vtt"` {
		t.Fatalf("download response = %d/%v/%q", downloadRecorder.Code, downloadRecorder.Header(), downloadRecorder.Body.String())
	}
	if auditService.calls != 1 || auditService.input.Action != "transcript_export.read" {
		t.Fatalf("audit = %+v/%d", auditService.input, auditService.calls)
	}
}

func TestArtifactReadAuditFailsClosedBeforeServingContent(t *testing.T) {
	service := &fakeClipService{media: clip.Media{
		ClipID: httpClipID, MIMEType: "audio/wav", Size: 10,
		SHA256: strings.Repeat("e", 64), Content: openHTTPAudioFixture(t, []byte("0123456789")),
	}}
	handler := artifactHandler(service, nil, &fakeAuditService{err: errors.New("audit unavailable")})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/audio-clips/"+httpClipID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "0123456789") {
		t.Fatalf("response = %d/%q", recorder.Code, recorder.Body.String())
	}
}

type fakeClipService struct {
	clip      clip.Clip
	media     clip.Media
	err       error
	assetID   string
	input     clip.CreateInput
	requestID string
}

func (service *fakeClipService) Create(
	_ context.Context, _ auth.Principal, assetID string, input clip.CreateInput, requestID string,
) (clip.Clip, error) {
	service.assetID, service.input, service.requestID = assetID, input, requestID
	return service.clip, service.err
}

func (service *fakeClipService) Open(context.Context, auth.Principal, string) (clip.Media, error) {
	return service.media, service.err
}

type fakeTranscriptExportService struct {
	export     transcriptexport.Export
	media      transcriptexport.Media
	err        error
	revisionID string
	input      transcriptexport.CreateInput
	requestID  string
}

func (service *fakeTranscriptExportService) Create(
	_ context.Context, _ auth.Principal, revisionID string,
	input transcriptexport.CreateInput, requestID string,
) (transcriptexport.Export, error) {
	service.revisionID, service.input, service.requestID = revisionID, input, requestID
	return service.export, service.err
}

func (service *fakeTranscriptExportService) Open(
	context.Context, auth.Principal, string,
) (transcriptexport.Media, error) {
	return service.media, service.err
}

func artifactHandler(
	clipService ClipService, exportService TranscriptExportService, auditService AuditService,
) http.Handler {
	return NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		AuthService: &fakeAuthService{principal: auth.Principal{
			UserID: "user-1", WorkspaceID: "workspace-1", Role: "agent",
			Scopes:         []string{auth.ScopeAudioRead, auth.ScopeTranscriptsRead, auth.ScopeMetadataWrite},
			CredentialType: "api_key", CredentialID: "key-1",
		}},
		ClipService: clipService, ExportService: exportService, AuditService: auditService,
	})
}

var (
	_ ClipService             = (*fakeClipService)(nil)
	_ TranscriptExportService = (*fakeTranscriptExportService)(nil)
)
