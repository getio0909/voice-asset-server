package systemsetting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/auth"
)

func TestServiceGetReturnsAllowlistedDeploymentProjection(t *testing.T) {
	service := NewService(Config{
		BrandName:                              "VoiceAsset Test",
		PublicOrigin:                           "https://voice.example.test",
		StorageBackend:                         "local",
		CookieSecure:                           true,
		ProviderCredentialEncryptionConfigured: true,
	})

	result, err := service.Get(context.Background(), auth.Principal{
		WorkspaceID: "10000000-0000-4000-8000-000000000001",
		Scopes:      []string{auth.ScopeAdminRead},
	})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if result.Scope != ScopeDeployment || result.Management != ManagementOperatorEnvironment || result.Mutable {
		t.Fatalf("unexpected boundary: %+v", result)
	}
	if result.BrandName != "VoiceAsset Test" || result.PublicOrigin != "https://voice.example.test" ||
		result.StorageBackend != "local" || !result.CookieSecure || !result.ProviderCredentialEncryptionConfigured {
		t.Fatalf("unexpected projection: %+v", result)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, forbidden := range []string{"database", "path", "endpoint", "access_key", "secret", "token"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("projection contains forbidden field %q: %s", forbidden, encoded)
		}
	}
}

func TestServiceGetRejectsPrincipalWithoutWorkspaceIdentity(t *testing.T) {
	service := NewService(Config{BrandName: "VoiceAsset"})

	_, err := service.Get(context.Background(), auth.Principal{
		WorkspaceID: "not-a-workspace",
		Scopes:      []string{auth.ScopeAdminRead},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Get() error = %v, want ErrForbidden", err)
	}
}
