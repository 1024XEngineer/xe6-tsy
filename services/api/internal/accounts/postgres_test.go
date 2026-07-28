package accounts

import (
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestRegisteredAccountInsertTargetsPartialPhoneIndex(t *testing.T) {
	if !strings.Contains(insertRegisteredAccountSQL, "ON CONFLICT (phone_hash) WHERE phone_hash IS NOT NULL DO NOTHING") {
		t.Fatalf("registered-account insert does not match the partial phone index: %s", insertRegisteredAccountSQL)
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
