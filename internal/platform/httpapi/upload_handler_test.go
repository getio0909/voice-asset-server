package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/upload"
)

func TestCreateUploadSessionReturnsResumableLocation(t *testing.T) {
	authService := writableAuthService()
	uploadService := &fakeUploadService{created: upload.Session{
		ID: "upload-1", AssetID: "asset-1", State: upload.StateActive, PartSize: upload.DefaultPartSize,
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, UploadService: uploadService,
		PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/uploads", strings.NewReader(`{
		"asset_id":"asset-1","filename":"recording.wav","mime_type":"audio/wav",
		"size_bytes":1024,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "upload-key")
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if recorder.Header().Get("Location") != "/api/v1/uploads/upload-1" {
		t.Fatalf("Location = %q", recorder.Header().Get("Location"))
	}
	if uploadService.idempotencyKey != "upload-key" || uploadService.createInput.AssetID != "asset-1" {
		t.Fatalf("service args = %+v/%q", uploadService.createInput, uploadService.idempotencyKey)
	}
}

func TestPutUploadPartStreamsOctetsAndIntegrityHeader(t *testing.T) {
	authService := writableAuthService()
	uploadService := &fakeUploadService{part: upload.Part{
		Number: 2, SizeBytes: 5, SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, UploadService: uploadService,
		PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/uploads/"+httpUploadID+"/parts/2", strings.NewReader("bytes"))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("X-Part-SHA256", uploadService.part.SHA256)
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if uploadService.putUploadID != httpUploadID || uploadService.putNumber != 2 || uploadService.putBody != "bytes" {
		t.Fatalf("PutPart args = %q/%d/%q", uploadService.putUploadID, uploadService.putNumber, uploadService.putBody)
	}
	if recorder.Header().Get("ETag") != `"`+uploadService.part.SHA256+`"` {
		t.Fatalf("ETag = %q", recorder.Header().Get("ETag"))
	}
}

func TestPutUploadPartMapsStreamedOversizeTo413(t *testing.T) {
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: writableAuthService(),
		UploadService: &fakeUploadService{putErr: upload.ErrPartTooLarge},
		PublicOrigin:  "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/uploads/"+httpUploadID+"/parts/1", strings.NewReader("oversized"))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("X-Part-SHA256", strings.Repeat("a", 64))
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}

func TestGetUploadSessionUsesBearerAuthentication(t *testing.T) {
	authService := writableAuthService()
	uploadService := &fakeUploadService{session: upload.Session{ID: "upload-1", State: upload.StateActive}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, UploadService: uploadService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/"+httpUploadID, nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || uploadService.getID != httpUploadID {
		t.Fatalf("GET upload = status %d, id %q", recorder.Code, uploadService.getID)
	}
}

func TestCompleteUploadReturnsCompletedSession(t *testing.T) {
	authService := writableAuthService()
	uploadService := &fakeUploadService{completed: upload.Session{
		ID: "upload-1", AssetID: "asset-1", State: upload.StateCompleted,
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, UploadService: uploadService,
		PublicOrigin: "https://voice.example.com",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/"+httpUploadID+"/complete", nil)
	request.Header.Set("Origin", "https://voice.example.com")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "va_test_token_with_sufficient_entropy"})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || uploadService.completeID != httpUploadID {
		t.Fatalf("complete upload = status %d, id %q: %s", recorder.Code, uploadService.completeID, recorder.Body.String())
	}
}

func writableAuthService() *fakeAuthService {
	return &fakeAuthService{principal: auth.Principal{
		UserID: "user-1", WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAssetsWrite},
	}}
}

type fakeUploadService struct {
	created          upload.Session
	replayedCreate   bool
	createErr        error
	createInput      upload.CreateInput
	idempotencyKey   string
	session          upload.Session
	getErr           error
	getID            string
	part             upload.Part
	replayedPart     bool
	putErr           error
	putUploadID      string
	putNumber        int
	putHash          string
	putBody          string
	completed        upload.Session
	replayedComplete bool
	completeErr      error
	completeID       string
}

func (f *fakeUploadService) Create(
	_ context.Context,
	_ auth.Principal,
	input upload.CreateInput,
	idempotencyKey string,
) (upload.Session, bool, error) {
	f.createInput = input
	f.idempotencyKey = idempotencyKey
	return f.created, f.replayedCreate, f.createErr
}

func (f *fakeUploadService) Get(_ context.Context, _ auth.Principal, uploadID string) (upload.Session, error) {
	f.getID = uploadID
	return f.session, f.getErr
}

func (f *fakeUploadService) PutPart(
	_ context.Context,
	_ auth.Principal,
	uploadID string,
	partNumber int,
	hash string,
	source io.Reader,
) (upload.Part, bool, error) {
	f.putUploadID = uploadID
	f.putNumber = partNumber
	f.putHash = hash
	content, _ := io.ReadAll(source)
	f.putBody = string(content)
	return f.part, f.replayedPart, f.putErr
}

func (f *fakeUploadService) Complete(
	_ context.Context,
	_ auth.Principal,
	uploadID string,
) (upload.Session, bool, error) {
	f.completeID = uploadID
	return f.completed, f.replayedComplete, f.completeErr
}

var _ UploadService = (*fakeUploadService)(nil)
