package delivery

import (
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestPostgresListMessageTargetsRejectsInvalidInput(t *testing.T) {
	repository := &PostgresRepository{}
	if _, err := repository.ListMessageTargets(t.Context(), "", nil); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("ListMessageTargets() error = %v, want invalid argument", err)
	}
	unsupported := Channel("sms")
	if _, err := repository.ListMessageTargets(t.Context(), "account-1", &unsupported); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("ListMessageTargets() unsupported channel error = %v, want invalid argument", err)
	}
}

func TestPostgresBindEmailTargetRejectsInvalidRecord(t *testing.T) {
	repository := &PostgresRepository{}
	_, err := repository.BindEmailTarget(t.Context(), BindEmailTargetRecord{})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("BindEmailTarget() error = %v, want invalid argument", err)
	}
}

func TestPostgresBindWeChatTargetRejectsInvalidRecord(t *testing.T) {
	repository := &PostgresRepository{}
	_, err := repository.BindWeChatTarget(t.Context(), BindWeChatTargetRecord{})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("BindWeChatTarget() error = %v, want invalid argument", err)
	}
}

func TestPostgresRevokeMessageTargetRejectsInvalidInput(t *testing.T) {
	repository := &PostgresRepository{}
	if err := repository.RevokeMessageTarget(t.Context(), "", ChannelEmail, "primary-email", testRevokeTime()); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("RevokeMessageTarget() empty account error = %v, want invalid argument", err)
	}
	if err := repository.RevokeMessageTarget(t.Context(), "account-1", ChannelEmail, "", testRevokeTime()); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("RevokeMessageTarget() empty ref error = %v, want invalid argument", err)
	}
	if err := repository.RevokeMessageTarget(t.Context(), "account-1", Channel("sms"), "primary-email", testRevokeTime()); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("RevokeMessageTarget() unsupported channel error = %v, want invalid argument", err)
	}
}

func testRevokeTime() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}
