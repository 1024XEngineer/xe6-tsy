package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("XE6_API_ADDRESS", "")
	t.Setenv("XE6_GIN_MODE", "")
	t.Setenv("PORT", "")

	cfg := Load()
	if cfg.Address != "127.0.0.1:8080" || cfg.Mode != "release" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadSupportsPortOverride(t *testing.T) {
	t.Setenv("XE6_API_ADDRESS", "127.0.0.1:9000")
	t.Setenv("PORT", "8081")

	if got := Load().Address; got != ":8081" {
		t.Fatalf("Address = %q, want %q", got, ":8081")
	}
}
