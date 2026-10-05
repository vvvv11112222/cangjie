package identity

import "testing"

func TestDevelopmentUsersRequestRequiresPassword(t *testing.T) {
	t.Setenv("BOOTSTRAP_TEST_PASSWORD", "")
	if _, err := DevelopmentUsersRequestFromEnvironment(); err == nil {
		t.Fatal("expected missing password error")
	}
}

func TestDevelopmentUsersRequestRejectsShortPassword(t *testing.T) {
	t.Setenv("BOOTSTRAP_TEST_PASSWORD", "short")
	if _, err := DevelopmentUsersRequestFromEnvironment(); err == nil {
		t.Fatal("expected short password error")
	}
}
