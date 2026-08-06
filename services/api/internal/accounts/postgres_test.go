package accounts

import (
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestRegisteredAccountInsertStoresOnlyPepperedDigest(t *testing.T) {
	for _, field := range []string{"phone_hash_v2", "ON CONFLICT DO NOTHING"} {
		if !strings.Contains(insertRegisteredAccountSQL, field) {
			t.Fatalf("registered-account insert does not store %q: %s", field, insertRegisteredAccountSQL)
		}
	}
	if strings.Contains(insertRegisteredAccountSQL, "phone_hash, phone_hash_v2") {
		t.Fatalf("registered-account insert persists a legacy digest: %s", insertRegisteredAccountSQL)
	}
}

func TestChallengeRateQueryIncludesLegacyUpgradeWindow(t *testing.T) {
	for _, predicate := range []string{"phone_hash = $1", "digest_version = 1", "phone_hash = $3"} {
		if !strings.Contains(challengeRateQuerySQL, predicate) {
			t.Fatalf("challenge rate query is missing %q: %s", predicate, challengeRateQuerySQL)
		}
	}
}

func TestRevokeSessionIsConditionalOnActiveState(t *testing.T) {
	if !strings.Contains(revokeActiveSessionSQL, "revoked_at IS NULL") {
		t.Fatalf("revoke SQL can update an already-revoked session: %s", revokeActiveSessionSQL)
	}
	if err := revokeSessionResult(0); err != domain.ErrNotFound {
		t.Fatalf("revokeSessionResult(0) = %v, want %v", err, domain.ErrNotFound)
	}
	if err := revokeSessionResult(1); err != nil {
		t.Fatalf("revokeSessionResult(1) = %v, want nil", err)
	}
}

func TestRotateSessionOnlyClaimsCurrentActiveOwner(t *testing.T) {
	for _, predicate := range []string{"account_id=$2", "revoked_at IS NULL", "expires_at > CURRENT_TIMESTAMP"} {
		if !strings.Contains(rotateActiveSessionSQL, predicate) {
			t.Fatalf("rotation SQL is missing %q: %s", predicate, rotateActiveSessionSQL)
		}
	}
}
