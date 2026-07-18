package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestGetAssetAudioServesAuthenticatedByteRange(t *testing.T) {
	file := openHTTPAudioFixture(t, []byte("0123456789"))
	authService := &fakeAuthService{principal: auth.Principal{
		WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead},
	}}
	audioService := &fakeAudioService{media: audio.Media{
		AssetID: "asset-1", MIMEType: "audio/wav", Size: 10,
		SHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Content: file,
	}}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset", AuthService: authService, AudioService: audioService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+httpAssetID+"/audio", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	request.Header.Set("Range", "bytes=2-5")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusPartialContent, recorder.Body.String())
	}
	if recorder.Body.String() != "2345" {
		t.Fatalf("body = %q, want %q", recorder.Body.String(), "2345")
	}
	if recorder.Header().Get("Content-Range") != "bytes 2-5/10" ||
		recorder.Header().Get("Accept-Ranges") != "bytes" ||
		recorder.Header().Get("Content-Type") != "audio/wav" ||
		recorder.Header().Get("ETag") != `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"` {
		t.Fatalf("media headers = %v", recorder.Header())
	}
	if audioService.assetID != httpAssetID {
		t.Fatalf("Open() asset ID = %q", audioService.assetID)
	}
}

func TestHeadAssetAudioReturnsMetadataWithoutBody(t *testing.T) {
	file := openHTTPAudioFixture(t, []byte("0123456789"))
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead},
		}},
		AudioService: &fakeAudioService{media: audio.Media{
			AssetID: "asset-1", MIMEType: "audio/wav", Size: 10,
			SHA256:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Content: file,
		}},
	})
	request := httptest.NewRequest(http.MethodHead, "/api/v1/assets/"+httpAssetID+"/audio", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("HEAD response = %d/%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Content-Length") != "10" {
		t.Fatalf("Content-Length = %q", recorder.Header().Get("Content-Length"))
	}
}

func TestAssetAudioHonorsETagConditions(t *testing.T) {
	const digest = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	for _, test := range []struct {
		name       string
		headerName string
		header     string
		wantStatus int
	}{
		{name: "not modified", headerName: "If-None-Match", header: `"` + digest + `"`, wantStatus: http.StatusNotModified},
		{name: "precondition failed", headerName: "If-Match", header: `"different"`, wantStatus: http.StatusPreconditionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := openHTTPAudioFixture(t, []byte("0123456789"))
			handler := NewApplicationHandler(Options{
				BrandName: "VoiceAsset",
				AuthService: &fakeAuthService{principal: auth.Principal{
					WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead},
				}},
				AudioService: &fakeAudioService{media: audio.Media{
					AssetID: "asset-1", MIMEType: "audio/wav", Size: 10,
					SHA256: digest, Content: file,
				}},
			})
			request := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+httpAssetID+"/audio", nil)
			request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
			request.Header.Set(test.headerName, test.header)
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; headers=%v body=%q", recorder.Code, test.wantStatus, recorder.Header(), recorder.Body.String())
			}
			if recorder.Header().Get("ETag") != `"`+digest+`"` {
				t.Fatalf("ETag = %q", recorder.Header().Get("ETag"))
			}
			if test.wantStatus == http.StatusNotModified && recorder.Body.Len() != 0 {
				t.Fatalf("304 body = %q, want empty", recorder.Body.String())
			}
			if test.wantStatus == http.StatusPreconditionFailed && recorder.Body.Len() != 0 {
				t.Fatalf("412 body = %q, want empty", recorder.Body.String())
			}
		})
	}
}

func TestAssetAudioMapsInvalidRangeAndWorkspaceAbsence(t *testing.T) {
	t.Run("invalid range", func(t *testing.T) {
		file := openHTTPAudioFixture(t, []byte("0123456789"))
		handler := NewApplicationHandler(Options{
			BrandName: "VoiceAsset",
			AuthService: &fakeAuthService{principal: auth.Principal{
				WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead},
			}},
			AudioService: &fakeAudioService{media: audio.Media{
				AssetID: "asset-1", MIMEType: "audio/wav", Size: 10,
				SHA256:  "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				Content: file,
			}},
		})
		request := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+httpAssetID+"/audio", nil)
		request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
		request.Header.Set("Range", "bytes=99-100")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusRequestedRangeNotSatisfiable ||
			recorder.Header().Get("Content-Range") != "bytes */10" {
			t.Fatalf("range response = %d/%v", recorder.Code, recorder.Header())
		}
	})

	t.Run("multiple ranges", func(t *testing.T) {
		file := openHTTPAudioFixture(t, []byte("0123456789"))
		handler := NewApplicationHandler(Options{
			BrandName: "VoiceAsset",
			AuthService: &fakeAuthService{principal: auth.Principal{
				WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead},
			}},
			AudioService: &fakeAudioService{media: audio.Media{
				AssetID: "asset-1", MIMEType: "audio/wav", Size: 10,
				SHA256:  "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
				Content: file,
			}},
		})
		request := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+httpAssetID+"/audio", nil)
		request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
		request.Header.Set("Range", "bytes=0-1,4-5")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusRequestedRangeNotSatisfiable ||
			recorder.Header().Get("Content-Range") != "bytes */10" ||
			!strings.HasPrefix(recorder.Header().Get("Content-Type"), "text/plain") {
			t.Fatalf("multiple-range response = %d/%v/%q", recorder.Code, recorder.Header(), recorder.Body.String())
		}
	})

	t.Run("workspace absence", func(t *testing.T) {
		handler := NewApplicationHandler(Options{
			BrandName: "VoiceAsset",
			AuthService: &fakeAuthService{principal: auth.Principal{
				WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead},
			}},
			AudioService: &fakeAudioService{err: audio.ErrAudioNotFound},
		})
		request := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+httpOtherAssetID+"/audio", nil)
		request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
		}
	})
}

type fakeAudioService struct {
	media   audio.Media
	err     error
	assetID string
}

func (f *fakeAudioService) Open(
	_ context.Context,
	_ auth.Principal,
	assetID string,
) (audio.Media, error) {
	f.assetID = assetID
	return f.media, f.err
}

func openHTTPAudioFixture(t *testing.T, content []byte) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write audio fixture: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audio fixture: %v", err)
	}
	return file
}

var _ AudioService = (*fakeAudioService)(nil)
