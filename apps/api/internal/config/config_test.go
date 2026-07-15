package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("XE6_API_ADDRESS", "")
	t.Setenv("XE6_GIN_MODE", "")
	t.Setenv("XE6_IDENTITY_PROVIDER", "")

	cfg := Load()
	if cfg.Address != "127.0.0.1:8080" || cfg.Mode != "release" || cfg.IdentityProvider != "disabled" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadEnablesMockIdentityProvider(t *testing.T) {
	t.Setenv("XE6_IDENTITY_PROVIDER", IdentityProviderMock)

	cfg := Load()
	if cfg.IdentityProvider != IdentityProviderMock {
		t.Fatalf("IdentityProvider = %q", cfg.IdentityProvider)
	}
}
