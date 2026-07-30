//go:build integration

package recordstore

import (
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestMessageTargetRepositoryBindListAndRevokeEmailTarget(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 10)
	}
	repository := delivery.NewPostgresRepository(pool)
	insertDeliveryAccount(t, pool, "target_bind_account", "anonymous", nil)

	service := delivery.NewPersistentUseCases(repository, nil, delivery.NewPostgresDestinationReader(pool, key), nil)
	service.ConfigureTargetBinding(key, "local")

	target, err := service.BindEmailTarget(t.Context(), "target_bind_account", "dev:primary-email:bind@example.test")
	if err != nil {
		t.Fatalf("BindEmailTarget() error = %v", err)
	}
	if target.DestinationRef != "primary-email" || !target.Verified || target.RevokedAt != nil {
		t.Fatalf("BindEmailTarget() = %#v", target)
	}

	targets, err := service.ListMessageTargets(t.Context(), "target_bind_account", nil)
	if err != nil {
		t.Fatalf("ListMessageTargets() error = %v", err)
	}
	if len(targets) != 1 || targets[0].DestinationRef != "primary-email" {
		t.Fatalf("ListMessageTargets() = %#v", targets)
	}

	destination, err := delivery.NewPostgresDestinationReader(pool, key).ResolveVerifiedDestination(
		t.Context(), "target_bind_account", delivery.ChannelEmail, "primary-email",
	)
	if err != nil {
		t.Fatalf("ResolveVerifiedDestination() error = %v", err)
	}
	if destination.ProviderTarget != "bind@example.test" {
		t.Fatalf("ProviderTarget = %q", destination.ProviderTarget)
	}

	if err := service.RevokeMessageTarget(t.Context(), "target_bind_account", delivery.ChannelEmail, "primary-email"); err != nil {
		t.Fatalf("RevokeMessageTarget() error = %v", err)
	}
	targets, err = service.ListMessageTargets(t.Context(), "target_bind_account", nil)
	if err != nil {
		t.Fatalf("ListMessageTargets() after revoke error = %v", err)
	}
	if len(targets) != 1 || targets[0].Verified || targets[0].RevokedAt == nil {
		t.Fatalf("ListMessageTargets() after revoke = %#v", targets)
	}

	_, err = delivery.NewPostgresDestinationReader(pool, key).ResolveVerifiedDestination(
		t.Context(), "target_bind_account", delivery.ChannelEmail, "primary-email",
	)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ResolveVerifiedDestination() after revoke error = %v, want not found", err)
	}
}

func TestMessageTargetRepositoryRevokeMissingTargetReturnsNotFound(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository := delivery.NewPostgresRepository(pool)
	insertDeliveryAccount(t, pool, "target_missing_account", "anonymous", nil)
	service := delivery.NewPersistentUseCases(repository, nil, nil, nil)
	if err := service.RevokeMessageTarget(t.Context(), "target_missing_account", delivery.ChannelEmail, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RevokeMessageTarget() error = %v, want not found", err)
	}
}
