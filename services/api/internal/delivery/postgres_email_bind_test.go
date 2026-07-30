package delivery

import (
	"errors"
	"testing"

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
