package providerprofile

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/platform/secretbox"
)

func TestResolverFallsBackToMockWhenWorkspaceHasNoEnabledProfile(t *testing.T) {
	repository := &fakeRepository{}
	resolver := NewResolver(repository, nil, nil)
	transcriber, err := resolver.Resolve(context.Background(), "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	result, err := transcriber.Transcribe(context.Background(), asr.Input{Language: "zh-CN"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderID != asr.MockProviderID || result.ProfileID != defaultMockProfileID {
		t.Fatalf("fallback metadata = %q/%q", result.ProviderID, result.ProfileID)
	}
}

func TestResolverDecryptsRecordBoundCredentialsAndBuildsOrderedRegistry(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, secretbox.KeyBytes)
	box, err := secretbox.New(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "10000000-0000-4000-8000-000000000001"
	profileID := "90000000-0000-4000-8000-000000000001"
	credentials := []byte(`{"secret_id":"fixture-secret-id","secret_key":"fixture-secret-key"}`)
	ciphertext, err := box.Seal(credentials, profileAssociatedData(workspaceID, profileID, asr.TencentProviderID))
	if err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{enabled: []StoredProfile{{
		Profile: Profile{
			ID: profileID, WorkspaceID: workspaceID, ProviderID: asr.TencentProviderID,
			DisplayName: "Tencent", Config: tencentConfig(), State: StateEnabled,
			Priority: 10, Version: 1, SecretConfigured: true,
		},
		SecretCiphertext: ciphertext,
	}}}
	resolver := NewResolver(repository, box, nil)
	transcriber, err := resolver.Resolve(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	registry, ok := transcriber.(*asr.Registry)
	if !ok {
		t.Fatalf("Resolve() returned %T", transcriber)
	}
	states := registry.Profiles()
	if len(states) != 1 || states[0].Profile.ID != profileID ||
		states[0].Profile.ProviderID != asr.TencentProviderID || !states[0].Active {
		t.Fatalf("resolved registry states = %+v", states)
	}
}

func TestResolverFailsClosedForMissingOrMismatchedEncryption(t *testing.T) {
	workspaceID := "workspace-1"
	profileID := "profile-1"
	repository := &fakeRepository{enabled: []StoredProfile{{
		Profile: Profile{
			ID: profileID, WorkspaceID: workspaceID, ProviderID: asr.TencentProviderID,
			Config: tencentConfig(), State: StateEnabled, SecretConfigured: true,
		},
		SecretCiphertext: []byte("invalid-ciphertext"),
	}}}
	for _, cipher := range []SecretCipher{nil, &fakeCipher{openErr: errors.New("authentication failed")}} {
		resolver := NewResolver(repository, cipher, nil)
		if _, err := resolver.Resolve(context.Background(), workspaceID); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("Resolve() error = %v, want ErrConfiguration", err)
		}
	}
}

func TestResolverRejectsCorruptStoredConfigWithoutCredentialsInError(t *testing.T) {
	config := tencentConfig()
	config.Timeout = "invalid"
	repository := &fakeRepository{enabled: []StoredProfile{{
		Profile: Profile{
			ID: "profile-1", WorkspaceID: "workspace-1", ProviderID: asr.TencentProviderID,
			Config: config, State: StateEnabled,
		},
	}}}
	resolver := NewResolver(repository, nil, nil)
	_, err := resolver.Resolve(context.Background(), "workspace-1")
	if !errors.Is(err, ErrConfiguration) {
		t.Fatalf("Resolve() error = %v", err)
	}
	if _, marshalErr := json.Marshal(err); marshalErr != nil {
		t.Fatal(marshalErr)
	}
}
