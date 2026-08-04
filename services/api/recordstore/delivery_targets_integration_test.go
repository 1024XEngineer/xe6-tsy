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

	reader, err := delivery.NewPostgresDestinationReader(pool, key)
	if err != nil {
		t.Fatalf("NewPostgresDestinationReader() error = %v", err)
	}
	service := delivery.NewPersistentUseCases(repository, nil, reader, nil)
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

	reader, err = delivery.NewPostgresDestinationReader(pool, key)
	if err != nil {
		t.Fatalf("NewPostgresDestinationReader() error = %v", err)
	}
	destination, err := reader.ResolveVerifiedDestination(
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

	reader, err = delivery.NewPostgresDestinationReader(pool, key)
	if err != nil {
		t.Fatalf("NewPostgresDestinationReader() error = %v", err)
	}
	_, err = reader.ResolveVerifiedDestination(
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

func TestMessagePreferencePersistsSelectedDestinationAndEnabled(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 20)
	}
	accountID := "preference_destination_account"
	insertDeliveryAccount(t, pool, accountID, "anonymous", nil)
	repository := delivery.NewPostgresRepository(pool)
	reader, err := delivery.NewPostgresDestinationReader(pool, key)
	if err != nil {
		t.Fatalf("NewPostgresDestinationReader() error = %v", err)
	}
	service := delivery.NewPersistentUseCases(repository, nil, reader, nil)
	service.ConfigureTargetBinding(key, "local")
	if _, err := service.BindEmailTarget(t.Context(), accountID, "dev:primary-email:preference@example.test"); err != nil {
		t.Fatalf("BindEmailTarget() error = %v", err)
	}

	preference, err := service.PutPreference(t.Context(), accountID, delivery.ChannelEmail, true)
	if err != nil {
		t.Fatalf("PutPreference() error = %v", err)
	}
	if preference.DestinationRef != "primary-email" || !preference.Enabled || !preference.Verified {
		t.Fatalf("stored preference = %#v, want selected verified destination and enabled", preference)
	}

	preferences, err := service.Preferences(t.Context(), accountID)
	if err != nil {
		t.Fatalf("Preferences() error = %v", err)
	}
	if len(preferences) != 1 || preferences[0].DestinationRef != "primary-email" || !preferences[0].Enabled || !preferences[0].Verified {
		t.Fatalf("listed preferences = %#v, want selected verified destination and enabled", preferences)
	}

	preference, err = service.PutPreferenceForDestination(
		t.Context(), accountID, delivery.ChannelEmail, false, "primary-email",
	)
	if err != nil {
		t.Fatalf("PutPreferenceForDestination(disable) error = %v", err)
	}
	if preference.DestinationRef != "primary-email" || preference.Enabled || !preference.Verified {
		t.Fatalf("disabled preference = %#v, want selected verified destination and disabled", preference)
	}
}
