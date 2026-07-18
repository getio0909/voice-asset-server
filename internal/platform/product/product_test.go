package product

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestContractVersionMatchesRepositoryPin(t *testing.T) {
	content, err := os.ReadFile("../../../CONTRACT_VERSION")
	if err != nil {
		t.Fatalf("read CONTRACT_VERSION: %v", err)
	}
	if got := strings.TrimSpace(string(content)); got != ContractVersion {
		t.Fatalf("CONTRACT_VERSION = %q, product = %q", got, ContractVersion)
	}
}

func TestCurrentBuildInfo(t *testing.T) {
	if got := CurrentBuildInfo(); got.ServerVersion != ServerVersion || got.Commit != Commit {
		t.Fatalf("CurrentBuildInfo() = %#v", got)
	}
}

func TestPhase1CapabilitiesAreSortedAndAdvertised(t *testing.T) {
	features := CurrentCapabilities().Features
	if !slices.IsSorted(features) {
		t.Fatalf("features are not sorted: %v", features)
	}
	for _, required := range []string{
		"authenticated_audio",
		"local_auth",
		"mock_asr",
		"raw_transcripts",
		"resumable_uploads",
		"transcription_jobs",
	} {
		if !slices.Contains(features, required) {
			t.Fatalf("required Phase 1 capability %q is missing", required)
		}
	}
}

func TestPhase2AdvertisesAndroidM4AUploads(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "m4a_uploads") {
		t.Fatal("m4a_uploads capability is missing")
	}
}

func TestPhase3AdvertisesManagedASRProviders(t *testing.T) {
	for _, feature := range []string{
		"aliyun_asr", "asr_failover", "asr_hotwords", "encrypted_provider_profiles",
		"llm_corrections", "llm_glossaries", "llm_provider_profiles", "tencent_asr", "transcript_approval",
	} {
		if !slices.Contains(CurrentCapabilities().Features, feature) {
			t.Fatalf("Phase 3 capability %q is missing", feature)
		}
	}
}

func TestPhase4AdvertisesAgentWorkflowCapabilities(t *testing.T) {
	for _, feature := range []string{"audio_clips", "organization_reads", "scoped_api_keys", "transcript_exports"} {
		if !slices.Contains(CurrentCapabilities().Features, feature) {
			t.Fatalf("Phase 4 capability %q is missing", feature)
		}
	}
}

func TestPhase5AdvertisesExpiredArtifactReaping(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "expired_artifact_reaping") {
		t.Fatal("expired_artifact_reaping capability is missing")
	}
}

func TestPhase6AdvertisesFullTextSearch(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "full_text_search") {
		t.Fatal("full_text_search capability is missing")
	}
}

func TestPhase7AdvertisesWaveforms(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "waveforms") {
		t.Fatal("waveforms capability is missing")
	}
}

func TestPhase8AdvertisesPermanentAssetPurge(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "asset_purge") {
		t.Fatal("asset_purge capability is missing")
	}
}

func TestPhase9AdvertisesMembershipManagement(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "membership_management") {
		t.Fatal("membership_management capability is missing")
	}
}

func TestPhase10AdvertisesWorkspaceManagement(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "workspace_management") {
		t.Fatal("workspace_management capability is missing")
	}
}

func TestPhase11AdvertisesAccountPasswordChange(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "account_password_change") {
		t.Fatal("account_password_change capability is missing")
	}
}

func TestPhase12AdvertisesIncrementalSync(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "incremental_sync") {
		t.Fatal("incremental_sync capability is missing")
	}
}

func TestMobileAdministrationAdvertisesJobRetry(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "administration_job_retry") {
		t.Fatal("administration_job_retry capability is missing")
	}
}

func TestDevicePairingCapabilityIsAdvertised(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "device_pairing") {
		t.Fatal("device_pairing capability is missing")
	}
}

func TestDeploymentSettingsReadCapabilityIsAdvertised(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "deployment_settings_read") {
		t.Fatal("deployment_settings_read capability is missing")
	}
}

func TestPersonalNotificationsCapabilityIsAdvertised(t *testing.T) {
	if !slices.Contains(CurrentCapabilities().Features, "personal_notifications") {
		t.Fatal("personal_notifications capability is missing")
	}
}
