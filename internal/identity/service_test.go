package identity

import "testing"

func TestCSRFTokenIsBoundToSession(t *testing.T) {
	a := CSRFToken("session-a")
	b := CSRFToken("session-b")
	if a == b || !SecureEqual(a, CSRFToken("session-a")) || SecureEqual(a, b) {
		t.Fatal("CSRF tokens must be stable per session and distinct between sessions")
	}
}

func TestAllowedActionsAreUnionWithoutPrivilegeComposition(t *testing.T) {
	college := "11111111-1111-1111-1111-111111111111"
	p := Principal{Roles: []RoleBinding{
		{RoleCode: "teacher"},
		{RoleCode: "supervisor", ScopeOrgID: &college},
	}}
	want := map[string]bool{"upload": true, "analyze": true, "review": true, "publish": true}
	got := p.AllowedActions()
	if len(got) != len(want) {
		t.Fatalf("actions = %v", got)
	}
	for _, action := range got {
		if !want[action] {
			t.Fatalf("unexpected action %q", action)
		}
	}
	for _, forbidden := range []string{"manage_academic", "manage_users", "operate", "delete"} {
		for _, action := range got {
			if action == forbidden {
				t.Fatalf("unexpected composed privilege %q", action)
			}
		}
	}
}
