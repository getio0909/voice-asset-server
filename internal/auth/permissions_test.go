package auth

import "testing"

func TestScopesForRoleEnforcesLeastPrivilege(t *testing.T) {
	owner := Principal{Role: "owner", Scopes: ScopesForRole("owner")}
	if !owner.Can(ScopeAssetsWrite) || !owner.Can(ScopeAdminWrite) {
		t.Fatalf("owner scopes = %v, want write access", owner.Scopes)
	}
	viewer := Principal{Role: "viewer", Scopes: ScopesForRole("viewer")}
	if !viewer.Can(ScopeAssetsRead) || !viewer.Can(ScopeAudioRead) {
		t.Fatalf("viewer scopes = %v, want read access", viewer.Scopes)
	}
	if viewer.Can(ScopeAssetsWrite) || viewer.Can(ScopeTranscriptionsWrite) {
		t.Fatalf("viewer scopes = %v, must not include write access", viewer.Scopes)
	}
	if scopes := ScopesForRole("unknown"); len(scopes) != 0 {
		t.Fatalf("unknown role scopes = %v, want none", scopes)
	}
}
