// Package systemsetting exposes a safe, read-only projection of deployment
// configuration without exposing the deployment-global system_settings table.
package systemsetting

import (
	"context"
	"errors"

	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/platform/identifier"
)

const (
	ScopeDeployment               = "deployment"
	ManagementOperatorEnvironment = "operator_environment"
)

var ErrForbidden = errors.New("deployment settings read is forbidden")

type Config struct {
	BrandName                              string
	PublicOrigin                           string
	StorageBackend                         string
	CookieSecure                           bool
	ProviderCredentialEncryptionConfigured bool
}

type Snapshot struct {
	Scope                                  string `json:"scope"`
	Management                             string `json:"management"`
	Mutable                                bool   `json:"mutable"`
	BrandName                              string `json:"brand_name"`
	PublicOrigin                           string `json:"public_origin"`
	StorageBackend                         string `json:"storage_backend"`
	CookieSecure                           bool   `json:"cookie_secure"`
	ProviderCredentialEncryptionConfigured bool   `json:"provider_credential_encryption_configured"`
}

type Service struct {
	snapshot Snapshot
}

func NewService(config Config) *Service {
	return &Service{snapshot: Snapshot{
		Scope:                                  ScopeDeployment,
		Management:                             ManagementOperatorEnvironment,
		Mutable:                                false,
		BrandName:                              config.BrandName,
		PublicOrigin:                           config.PublicOrigin,
		StorageBackend:                         config.StorageBackend,
		CookieSecure:                           config.CookieSecure,
		ProviderCredentialEncryptionConfigured: config.ProviderCredentialEncryptionConfigured,
	}}
}

func (service *Service) Get(_ context.Context, principal auth.Principal) (Snapshot, error) {
	if !principal.Can(auth.ScopeAdminRead) {
		return Snapshot{}, ErrForbidden
	}
	if _, ok := identifier.NormalizeUUID(principal.WorkspaceID); !ok {
		return Snapshot{}, ErrForbidden
	}
	return service.snapshot, nil
}
