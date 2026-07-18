package transcriptexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
)

const (
	exportAssetID     = "10000000-0000-4000-8000-000000000011"
	exportRevisionID  = "20000000-0000-4000-8000-000000000011"
	exportWorkspaceID = "30000000-0000-4000-8000-000000000011"
	exportUserID      = "40000000-0000-4000-8000-000000000011"
	exportAPIKeyID    = "50000000-0000-4000-8000-000000000011"
)

func TestRenderFormats(t *testing.T) {
	revision := exportRevision()
	for _, test := range []struct {
		format   string
		mimeType string
		contains []string
	}{
		{
			format: FormatJSON, mimeType: "application/json",
			contains: []string{`"id": "` + exportRevisionID + `"`, `"start_ms": 250`},
		},
		{
			format: FormatMarkdown, mimeType: "text/markdown; charset=utf-8",
			contains: []string{"# Transcript " + exportRevisionID, "- [250,1250) First line"},
		},
		{
			format: FormatSRT, mimeType: "application/x-subrip; charset=utf-8",
			contains: []string{"1\n00:00:00,250 --> 00:00:01,250\nFirst line", "2\n01:02:03,004 --> 01:02:04,050"},
		},
		{
			format: FormatVTT, mimeType: "text/vtt; charset=utf-8",
			contains: []string{"WEBVTT\n\n", "00:00:00.250 --> 00:00:01.250", "01:02:03.004 --> 01:02:04.050"},
		},
	} {
		t.Run(test.format, func(t *testing.T) {
			content, mimeType, err := render(revision, test.format)
			if err != nil {
				t.Fatalf("render() error = %v", err)
			}
			if mimeType != test.mimeType {
				t.Fatalf("MIME type = %q, want %q", mimeType, test.mimeType)
			}
			for _, fragment := range test.contains {
				if !bytes.Contains(content, []byte(fragment)) {
					t.Fatalf("content does not contain %q:\n%s", fragment, content)
				}
			}
		})
	}
}

func TestServiceCreateStoresRevisionExportAndAttributesAgent(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeExportRepository{}
	source := &fakeRevisionSource{revision: exportRevision()}
	service := NewService(repository, source, store)
	service.random = bytes.NewReader(make([]byte, 32))
	service.now = func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) }

	created, err := service.Create(
		context.Background(), exportPrincipal(auth.ScopeTranscriptsRead, auth.ScopeMetadataWrite),
		exportRevisionID, CreateInput{Format: " SRT "}, "request-export-1",
	)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Format != FormatSRT || created.DownloadURL != "/api/v1/transcript-exports/"+created.ID ||
		created.ExpiresAt != service.now().Add(ExportLifetime) {
		t.Fatalf("created export = %+v", created)
	}
	params := repository.createParams
	if params.ActorType != "agent" || params.ActorID != exportUserID || params.CredentialID != exportAPIKeyID ||
		params.RequestID != "request-export-1" || params.AssetID != exportAssetID ||
		params.RevisionID != exportRevisionID || params.StorageBackend != storage.BackendLocal ||
		params.MIMEType != "application/x-subrip; charset=utf-8" {
		t.Fatalf("repository params = %+v", params)
	}
	file, err := store.Open(context.Background(), params.StorageKey)
	if err != nil {
		t.Fatalf("open stored export: %v", err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil || !bytes.Contains(content, []byte("00:00:00,250 --> 00:00:01,250")) {
		t.Fatalf("stored export = %q, error = %v", content, err)
	}
}

func TestServiceCreateRejectsScopesAndInvalidInputBeforeLoadingRevision(t *testing.T) {
	source := &fakeRevisionSource{}
	service := NewService(&fakeExportRepository{}, source, &fakeExportStore{})
	for _, test := range []struct {
		name      string
		principal auth.Principal
		revision  string
		format    string
		requestID string
		want      error
	}{
		{
			name: "missing write scope", principal: exportPrincipal(auth.ScopeTranscriptsRead),
			revision: exportRevisionID, format: FormatJSON, requestID: "request", want: ErrForbidden,
		},
		{
			name: "invalid revision", principal: exportPrincipal(auth.ScopeTranscriptsRead, auth.ScopeMetadataWrite),
			revision: "not-a-uuid", format: FormatJSON, requestID: "request", want: ErrInvalidInput,
		},
		{
			name: "invalid format", principal: exportPrincipal(auth.ScopeTranscriptsRead, auth.ScopeMetadataWrite),
			revision: exportRevisionID, format: "html", requestID: "request", want: ErrInvalidInput,
		},
		{
			name: "invalid request ID", principal: exportPrincipal(auth.ScopeTranscriptsRead, auth.ScopeMetadataWrite),
			revision: exportRevisionID, format: FormatJSON, requestID: "bad\nrequest", want: ErrInvalidInput,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.Create(
				context.Background(), test.principal, test.revision,
				CreateInput{Format: test.format}, test.requestID,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want %v", err, test.want)
			}
		})
	}
	if source.calls != 0 {
		t.Fatalf("source calls = %d, want 0", source.calls)
	}
}

func TestServiceOpenVerifiesStoredExport(t *testing.T) {
	content := []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nHello\n")
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	object, err := store.PutImmutable(
		context.Background(), exportAssetID, exportRevisionID, storage.ObjectKindExport,
		bytes.NewReader(content), int64(len(content)),
	)
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeExportRepository{stored: StoredExport{
		Export: Export{
			ID: exportRevisionID, Format: FormatVTT, MIMEType: "text/vtt; charset=utf-8",
			FileSize: object.Size, SHA256: object.SHA256,
		},
		StorageBackend: object.Backend, StorageKey: object.Key,
	}}
	service := NewService(repository, nil, store)

	media, err := service.Open(
		context.Background(), exportPrincipal(auth.ScopeTranscriptsRead), exportRevisionID,
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer media.Content.Close()
	got, err := io.ReadAll(media.Content)
	if err != nil || !bytes.Equal(got, content) || media.Extension != "vtt" {
		t.Fatalf("Open() = %q/%+v, error = %v", got, media, err)
	}
	if repository.getWorkspaceID != exportWorkspaceID {
		t.Fatalf("workspace = %q", repository.getWorkspaceID)
	}
}

type fakeExportRepository struct {
	createParams   CreateParams
	createErr      error
	stored         StoredExport
	getErr         error
	getWorkspaceID string
}

func (repository *fakeExportRepository) Create(_ context.Context, params CreateParams) (Export, error) {
	repository.createParams = params
	return Export{
		ID: params.ID, AssetID: params.AssetID, RevisionID: params.RevisionID, Format: params.Format,
		MIMEType: params.MIMEType, FileSize: params.FileSize, SHA256: params.SHA256,
		CreatedAt: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC), ExpiresAt: params.ExpiresAt,
	}, repository.createErr
}

func (repository *fakeExportRepository) Get(
	_ context.Context, workspaceID, _ string, _ time.Time,
) (StoredExport, error) {
	repository.getWorkspaceID = workspaceID
	return repository.stored, repository.getErr
}

type fakeRevisionSource struct {
	revision transcript.Revision
	err      error
	calls    int
}

func (source *fakeRevisionSource) GetRevision(
	context.Context, auth.Principal, string,
) (transcript.Revision, error) {
	source.calls++
	return source.revision, source.err
}

type fakeExportStore struct{}

func (*fakeExportStore) PutImmutable(context.Context, string, string, string, io.Reader, int64) (storage.Object, error) {
	return storage.Object{}, errors.New("unexpected PutImmutable call")
}
func (*fakeExportStore) Backend() storage.Backend                                  { return storage.BackendLocal }
func (*fakeExportStore) DeleteObject(context.Context, string, int64, string) error { return nil }
func (*fakeExportStore) Open(context.Context, string) (storage.File, error) {
	return nil, os.ErrNotExist
}

func exportPrincipal(scopes ...string) auth.Principal {
	return auth.Principal{
		UserID: exportUserID, WorkspaceID: exportWorkspaceID, Role: "agent", Scopes: scopes,
		CredentialType: "api_key", CredentialID: exportAPIKeyID,
	}
}

func exportRevision() transcript.Revision {
	return transcript.Revision{
		ID: exportRevisionID, TranscriptID: "60000000-0000-4000-8000-000000000011",
		AssetID: exportAssetID, Kind: transcript.KindApproved, Language: "en-US",
		Text: "First line\nSecond line", ReviewStatus: "approved", CreatedByType: "agent",
		ProviderSnapshot: json.RawMessage(`{}`), HotwordSnapshot: json.RawMessage(`[]`),
		GlossarySnapshot: json.RawMessage(`[]`), Diff: json.RawMessage(`{}`),
		ValidationResult: json.RawMessage(`{"valid":true}`),
		Segments: []transcript.Segment{
			{ID: "70000000-0000-4000-8000-000000000011", Ordinal: 0, StartMS: 250, EndMS: 1_250, Text: "First line", Words: json.RawMessage(`[]`)},
			{ID: "70000000-0000-4000-8000-000000000012", Ordinal: 1, StartMS: 3_723_004, EndMS: 3_724_050, Text: "Second\r\nline", Words: json.RawMessage(`[]`)},
		},
	}
}

var (
	_ Repository     = (*fakeExportRepository)(nil)
	_ RevisionSource = (*fakeRevisionSource)(nil)
	_ Store          = (*fakeExportStore)(nil)
)
