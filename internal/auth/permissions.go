package auth

const (
	ScopeAssetsRead          = "assets:read"
	ScopeAudioRead           = "audio:read"
	ScopeAssetsWrite         = "assets:write"
	ScopeTranscriptsRead     = "transcripts:read"
	ScopeTranscriptionsWrite = "transcriptions:write"
	ScopeCorrectionsWrite    = "corrections:write"
	ScopeMetadataWrite       = "metadata:write"
	ScopeAdminRead           = "admin:read"
	ScopeAdminWrite          = "admin:write"
)

var allScopes = []string{
	ScopeAdminRead,
	ScopeAdminWrite,
	ScopeAssetsRead,
	ScopeAssetsWrite,
	ScopeAudioRead,
	ScopeCorrectionsWrite,
	ScopeMetadataWrite,
	ScopeTranscriptionsWrite,
	ScopeTranscriptsRead,
}

func AllScopes() []string {
	return append([]string(nil), allScopes...)
}

func ValidScope(scope string) bool {
	for _, candidate := range allScopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

func ScopesForRole(role string) []string {
	var scopes []string
	switch role {
	case "owner", "admin":
		scopes = allScopes
	case "editor":
		scopes = []string{
			ScopeAssetsRead, ScopeAssetsWrite, ScopeAudioRead, ScopeCorrectionsWrite,
			ScopeMetadataWrite, ScopeTranscriptionsWrite, ScopeTranscriptsRead,
		}
	case "viewer", "agent":
		scopes = []string{ScopeAssetsRead, ScopeAudioRead, ScopeTranscriptsRead}
	default:
		return []string{}
	}
	return append([]string(nil), scopes...)
}

func (p Principal) Can(scope string) bool {
	for _, candidate := range p.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}
