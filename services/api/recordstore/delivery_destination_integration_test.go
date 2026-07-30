//go:build integration

package recordstore

import (
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresDestinationReaderResolvesVerifiedTarget(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	key := testDestinationKey(t)
	target := "member5-delivery@example.test"
	ciphertext, err := delivery.EncryptProviderTarget(key, target)
	if err != nil {
		t.Fatalf("EncryptProviderTarget() error = %v", err)
	}

	insertDeliveryAccount(t, pool, "destination_account", "anonymous", nil)
	insertVerifiedDestination(t, pool, "destination_account", "primary-email", ciphertext)

	reader, err := delivery.NewPostgresDestinationReader(pool, key)
	if err != nil {
		t.Fatalf("NewPostgresDestinationReader() error = %v", err)
	}

	destination, err := reader.ResolveVerifiedDestination(t.Context(), "destination_account", delivery.ChannelEmail, "primary-email")
	if err != nil {
		t.Fatalf("ResolveVerifiedDestination() error = %v", err)
	}
	if destination.ProviderTarget != target {
		t.Fatalf("ProviderTarget = %q, want %q", destination.ProviderTarget, target)
	}
}

func TestPostgresDestinationReaderRejectsUnverifiedOrRevokedTargets(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	key := testDestinationKey(t)
	ciphertext, err := delivery.EncryptProviderTarget(key, "pending@example.test")
	if err != nil {
		t.Fatalf("EncryptProviderTarget() error = %v", err)
	}

	insertDeliveryAccount(t, pool, "destination_pending", "anonymous", nil)
	insertDeliveryAccount(t, pool, "destination_revoked", "anonymous", nil)

	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO account_destinations (
			id, account_id, channel, destination_ref, provider_target_ciphertext,
			key_version, created_at, updated_at
		) VALUES ($1, $2, 'email', 'pending-email', $3, 'v1', $4, $4)`,
		"dest_pending", "destination_pending", ciphertext, now,
	); err != nil {
		t.Fatalf("insert pending destination: %v", err)
	}

	revokedAt := now.Add(time.Minute)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO account_destinations (
			id, account_id, channel, destination_ref, provider_target_ciphertext,
			key_version, verified_at, revoked_at, created_at, updated_at
		) VALUES ($1, $2, 'email', 'revoked-email', $3, 'v1', $4, $5, $4, $5)`,
		"dest_revoked", "destination_revoked", ciphertext, now, revokedAt,
	); err != nil {
		t.Fatalf("insert revoked destination: %v", err)
	}

	reader, err := delivery.NewPostgresDestinationReader(pool, key)
	if err != nil {
		t.Fatalf("NewPostgresDestinationReader() error = %v", err)
	}

	tests := []struct {
		name      string
		accountID string
		reference string
		wantErr   error
	}{
		{name: "missing destination", accountID: "destination_pending", reference: "missing-email", wantErr: domain.ErrNotFound},
		{name: "pending verification", accountID: "destination_pending", reference: "pending-email", wantErr: domain.ErrNotFound},
		{name: "revoked destination", accountID: "destination_revoked", reference: "revoked-email", wantErr: domain.ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := reader.ResolveVerifiedDestination(t.Context(), test.accountID, delivery.ChannelEmail, test.reference)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ResolveVerifiedDestination() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func testDestinationKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	return key
}

func insertVerifiedDestination(t *testing.T, pool *pgxpool.Pool, accountID, reference string, ciphertext []byte) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO account_destinations (
			id, account_id, channel, destination_ref, provider_target_ciphertext,
			key_version, verified_at, created_at, updated_at
		) VALUES ($1, $2, 'email', $3, $4, 'v1', $5, $5, $5)`,
		"dest_"+accountID+"_"+reference, accountID, reference, ciphertext, now,
	); err != nil {
		t.Fatalf("insert verified destination: %v", err)
	}
}
