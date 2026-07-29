package main

import (
	"context"
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/config"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
)

func TestConfiguredProviderDefaultsToFailClosed(t *testing.T) {
	provider, err := configuredProvider("unconfigured")
	if err != nil {
		t.Fatalf("configuredProvider() error = %v", err)
	}
	idempotent, ok := provider.(delivery.IdempotentProvider)
	if provider == nil || !ok || idempotent.SupportsProviderIdempotency() {
		t.Fatal("unconfigured provider must be non-idempotent and non-nil")
	}
	if err := provider.Send(t.Context(), delivery.SendRequest{}); !errors.Is(err, delivery.ErrProviderNotConfigured) {
		t.Fatalf("Send() error = %v, want ErrProviderNotConfigured", err)
	}
}

func TestConfiguredProviderRejectsUnknownName(t *testing.T) {
	if _, err := configuredProvider("smtp"); err == nil {
		t.Fatal("configuredProvider() succeeded for unknown provider")
	}
}

func TestConfiguredRuntimeHonorsCanceledStartupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := newConfiguredRuntime(ctx, config.Config{
		DatabaseURL: "postgres://127.0.0.1:1/lingow",
	})
	if err == nil {
		t.Fatal("newConfiguredRuntime() succeeded with canceled startup context")
	}
}
