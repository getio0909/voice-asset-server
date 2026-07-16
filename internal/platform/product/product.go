// Package product centralizes the product and public API identity.
package product

const (
	// Name is the default user-facing product name.
	Name = "VoiceAsset"

	// APIVersion is the stable REST API namespace exposed by this server.
	APIVersion = "v1"

	// ContractVersion changes when the OpenAPI contract changes.
	ContractVersion = "0.1.0"
)

// These values may be replaced at build time with -ldflags.
var (
	ServerVersion = "0.1.0-dev"
	Commit        = "unknown"
)

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
			"capability_negotiation",
			"health_checks",
			"request_ids",
			"structured_errors",
		},
	}
}
