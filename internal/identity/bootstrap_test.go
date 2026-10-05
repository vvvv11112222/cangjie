package identity

import "testing"

func TestBootstrapRequestRequiresPassword(t *testing.T) {
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "")
	if _, err := BootstrapRequestFromEnvironment(); err == nil {
		t.Fatal("expected missing password error")
	}
}

func TestBootstrapRequestRejectsShortPassword(t *testing.T) {
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "short")
	if _, err := BootstrapRequestFromEnvironment(); err == nil {
		t.Fatal("expected short password error")
	}
}
