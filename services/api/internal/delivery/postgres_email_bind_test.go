package delivery

import (
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestPostgresCreateEmailBindChallengeRejectsInvalidRecord(t *testing.T) {
	repository := &PostgresRepository{}
	if err := repository.CreateEmailBindChallenge(t.Context(), EmailBindChallenge{}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("CreateEmailBindChallenge() error = %v, want invalid argument", err)
	}
}

func TestPostgresConsumeEmailBindChallengeRejectsInvalidInput(t *testing.T) {
	repository := &PostgresRepository{}
	if _, err := repository.ConsumeEmailBindChallenge(t.Context(), "", "hash"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("ConsumeEmailBindChallenge() empty account error = %v, want invalid argument", err)
	}
	if _, err := repository.ConsumeEmailBindChallenge(t.Context(), "account-1", ""); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("ConsumeEmailBindChallenge() empty token error = %v, want invalid argument", err)
	}
}

func TestPostgresRestoreEmailBindChallengeRejectsEmptyID(t *testing.T) {
	repository := &PostgresRepository{}
	if err := repository.RestoreEmailBindChallenge(t.Context(), ""); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("RestoreEmailBindChallenge() error = %v, want invalid argument", err)
	}
}

func TestCheckEmailBindRateLimitUsesEnforcement(t *testing.T) {
	latest := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	now := latest.Add(30 * time.Second)
	err := enforceEmailBindRateLimit(&latest, 1, now)
	if !errors.Is(err, domain.ErrRateLimited) {
		t.Fatalf("enforceEmailBindRateLimit() error = %v, want rate limited", err)
	}
}
