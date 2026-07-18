package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/waveform"
)

func TestGetAssetWaveformServesInlinePNGAndAudits(t *testing.T) {
	content := []byte("0123456789-waveform-png")
	file := openHTTPAudioFixture(t, content)
	service := &fakeWaveformService{media: waveform.Media{
		ObjectID: "30000000-0000-4000-8000-000000000001", AssetID: httpAssetID,
		MIMEType: "image/png", Size: int64(len(content)),
		SHA256:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Content: file,
	}}
	auditService := &fakeAuditService{}
	handler := NewApplicationHandler(Options{
		BrandName: "VoiceAsset",
		AuthService: &fakeAuthService{principal: auth.Principal{
			WorkspaceID: "workspace-1", Scopes: []string{auth.ScopeAudioRead},
		}},
		WaveformService: service, AuditService: auditService,
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+httpAssetID+"/waveform", nil)
	request.Header.Set("Authorization", "Bearer va_test_token_with_sufficient_entropy")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != string(content) ||
		recorder.Header().Get("Content-Type") != "image/png" ||
		recorder.Header().Get("Content-Disposition") != `inline; filename="`+httpAssetID+`.png"` {
		t.Fatalf("waveform response = %d/%v/%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}
	if service.assetID != httpAssetID || auditService.calls != 1 ||
		auditService.input.Action != "waveform.read" || auditService.input.TargetID != service.media.ObjectID {
		t.Fatalf("service/audit = %q/%+v", service.assetID, auditService.input)
	}
}

type fakeWaveformService struct {
	media   waveform.Media
	err     error
	assetID string
}

func (service *fakeWaveformService) Open(_ context.Context, _ auth.Principal, assetID string) (waveform.Media, error) {
	service.assetID = assetID
	return service.media, service.err
}
