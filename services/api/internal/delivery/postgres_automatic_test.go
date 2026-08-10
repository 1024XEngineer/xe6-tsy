package delivery

import (
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestAutomaticPostgresRepositoriesRejectInvalidInputs(t *testing.T) {
	repository := &PostgresRepository{}
	if _, err := repository.GetAutomaticTurnRun(t.Context(), "", "turn-1"); err != domain.ErrInvalidArgument {
		t.Fatalf("GetAutomaticTurnRun() error = %v, want invalid argument", err)
	}
	if err := repository.ScheduleAutomaticTurn(t.Context(), AutomaticTurnScheduleRecord{}); err != domain.ErrInvalidArgument {
		t.Fatalf("ScheduleAutomaticTurn() error = %v, want invalid argument", err)
	}
	if _, err := repository.ListAutomaticTurnRetryCandidates(t.Context(), 0); err != domain.ErrInvalidArgument {
		t.Fatalf("ListAutomaticTurnRetryCandidates() error = %v, want invalid argument", err)
	}
	if _, err := repository.ListAutomaticOutputStatus(t.Context(), "account-1", "", 1); err != domain.ErrInvalidArgument {
		t.Fatalf("ListAutomaticOutputStatus() error = %v, want invalid argument", err)
	}
	if _, err := repository.ListAutomaticTurnSettlements(t.Context(), "", "turn-1"); err != domain.ErrInvalidArgument {
		t.Fatalf("ListAutomaticTurnSettlements() error = %v, want invalid argument", err)
	}
	if _, err := repository.RetryAutomaticTurnTarget(t.Context(), "account-1", "turn-1", "", "retry-1"); err != domain.ErrInvalidArgument {
		t.Fatalf("RetryAutomaticTurnTarget() error = %v, want invalid argument", err)
	}
	if _, err := repository.ListAutomaticTurnRecoveryCandidates(t.Context(), 0); err != domain.ErrInvalidArgument {
		t.Fatalf("ListAutomaticTurnRecoveryCandidates() error = %v, want invalid argument", err)
	}
	if _, err := repository.ListAutomaticTurnRestoreCandidates(t.Context(), 0); err != domain.ErrInvalidArgument {
		t.Fatalf("ListAutomaticTurnRestoreCandidates() error = %v, want invalid argument", err)
	}
	if _, _, err := repository.ClaimAutomaticTurnFallback(t.Context(), "", "turn-1"); err != domain.ErrInvalidArgument {
		t.Fatalf("ClaimAutomaticTurnFallback() error = %v, want invalid argument", err)
	}
	if err := repository.MarkAutomaticTurnFallbackPlayed(t.Context(), "account-1", ""); err != domain.ErrInvalidArgument {
		t.Fatalf("MarkAutomaticTurnFallbackPlayed() error = %v, want invalid argument", err)
	}
	if err := repository.MarkAutomaticTurnRestored(t.Context(), "", "turn-1"); err != domain.ErrInvalidArgument {
		t.Fatalf("MarkAutomaticTurnRestored() error = %v, want invalid argument", err)
	}
}
