// Package product centralizes the product and public API identity.
package product

const (
	// Name is the default user-facing product name.
	Name = "VoiceAsset"

	// APIVersion is the stable REST API namespace exposed by this server.
	APIVersion = "v1"

	// ContractVersion changes when the OpenAPI contract changes.
	ContractVersion = "0.22.0"
)

// These values may be replaced at build time with -ldflags.
var (
	ServerVersion = "0.1.0-dev"
	Commit        = "unknown"
)

// BuildInfo identifies the source revision used for a Server binary.
type BuildInfo struct {
	ServerVersion string `json:"server_version"`
	Commit        string `json:"commit"`
}

// CurrentBuildInfo returns the values embedded by the release build.
func CurrentBuildInfo() BuildInfo {
	return BuildInfo{ServerVersion: ServerVersion, Commit: Commit}
}

// Capabilities is the stable wire representation consumed by clients.
type Capabilities struct {
	ServerVersion   string   `json:"server_version"`
	APIVersion      string   `json:"api_version"`
	ContractVersion string   `json:"contract_version"`
	Features        []string `json:"features"`
}

// CurrentCapabilities reports only features implemented by this build.
func CurrentCapabilities() Capabilities {
	return Capabilities{
		ServerVersion:   ServerVersion,
		APIVersion:      APIVersion,
		ContractVersion: ContractVersion,
		Features: []string{
			"account_password_change",
			"admin_operations",
			"administration_job_retry",
			"aliyun_asr",
			"asr_failover",
			"asr_hotwords",
			"asset_filters",
			"asset_lifecycle",
			"asset_purge",
			"asset_search",
			"audio_clips",
			"authenticated_audio",
			"capability_negotiation",
			"deployment_settings_read",
			"device_pairing",
			"device_sessions",
			"encrypted_provider_profiles",
			"expired_artifact_reaping",
			"full_text_search",
			"health_checks",
			"incremental_sync",
			"llm_corrections",
			"llm_glossaries",
			"llm_provider_profiles",
			"local_auth",
			"m4a_uploads",
			"membership_management",
			"metadata_mutations",
			"mock_asr",
			"organization_reads",
			"outbound_webhooks",
			"personal_notifications",
			"raw_transcripts",
			"realtime_transcription",
			"refresh_sessions",
			"request_ids",
			"resumable_uploads",
			"scoped_api_keys",
			"structured_errors",
			"tencent_asr",
			"transcript_approval",
			"transcript_exports",
			"transcription_jobs",
			"waveforms",
			"workspace_management",
		},
	}
}
