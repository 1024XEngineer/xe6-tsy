package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("XE6_API_ADDRESS", "")
	t.Setenv("XE6_GIN_MODE", "")

	cfg := Load()
	if cfg.Address != "127.0.0.1:8080" || cfg.Mode != "release" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}
