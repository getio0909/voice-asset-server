package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/platform/product"
)

func TestHealthEndpoints(t *testing.T) {
	for _, path := range []string{"/healthz", "/livez", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, nil)

			NewHandler("VoiceAsset", nil).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			if recorder.Header().Get("X-Request-ID") == "" {
				t.Fatal("X-Request-ID is empty")
			}
			var response healthResponse
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Status != "ok" || response.Service != "VoiceAsset" || response.Timestamp == "" {
				t.Fatalf("unexpected response: %+v", response)
			}
		})
	}
}

func TestCapabilitiesWireContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/capabilities", nil)
	request.Header.Set("X-Request-ID", "client-request-id")

	NewHandler("VoiceAsset", nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder.Header().Get("X-Request-ID") != "client-request-id" {
		t.Fatalf("X-Request-ID = %q", recorder.Header().Get("X-Request-ID"))
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	wantKeys := []string{"api_version", "contract_version", "features", "server_version"}
	gotKeys := make([]string, 0, len(raw))
	for _, key := range wantKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing response key %q", key)
		}
		gotKeys = append(gotKeys, key)
	}
	if len(raw) != len(wantKeys) || !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("response keys = %v, want exactly %v", gotKeys, wantKeys)
	}

	var response product.Capabilities
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if response.APIVersion != product.APIVersion || response.ContractVersion != product.ContractVersion {
		t.Fatalf("unexpected versions: %+v", response)
	}
	if len(response.Features) == 0 {
		t.Fatal("features is empty")
	}
}

func TestErrorsUseUnifiedEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/healthz", nil)

	NewHandler("VoiceAsset", nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if recorder.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", recorder.Header().Get("Allow"))
	}
	var response errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "method_not_allowed" || response.Error.RequestID == "" {
		t.Fatalf("unexpected error response: %+v", response)
	}
}

func TestOversizedRequestIDIsReplaced(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("X-Request-ID", string(make([]byte, maxRequestIDLength+1)))

	NewHandler("VoiceAsset", nil).ServeHTTP(recorder, request)

	requestID := recorder.Header().Get("X-Request-ID")
	if requestID == "" || len(requestID) > maxRequestIDLength {
		t.Fatalf("X-Request-ID length = %d, want 1..%d", len(requestID), maxRequestIDLength)
	}
}
