package delivery

import (
	"context"
	"errors"
	"testing"
)

func TestAutomaticTurnBatchOperationsUseDefaultLimit(t *testing.T) {
	repository := &automaticWorkerRepository{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticFallback(&fallbackPlayerFake{})
	service.ConfigureAutomaticOutputRestorer(&outputRestorerFake{})

	if err := service.RetryAutomaticTurns(t.Context(), 0); err != nil {
		t.Fatalf("RetryAutomaticTurns() error = %v", err)
	}
	if err := service.RecoverAutomaticTurns(t.Context(), 0); err != nil {
		t.Fatalf("RecoverAutomaticTurns() error = %v", err)
	}
	if err := service.RestoreAutomaticTurns(t.Context(), 0); err != nil {
		t.Fatalf("RestoreAutomaticTurns() error = %v", err)
	}
	if repository.retryLimit != 20 || repository.recoveryLimit != 20 || repository.restoreLimit != 20 {
		t.Fatalf("batch limits = retry %d, recovery %d, restore %d; want 20", repository.retryLimit, repository.recoveryLimit, repository.restoreLimit)
	}
}

func TestAutomaticTurnBatchOperationsPropagateCandidateErrors(t *testing.T) {
	tests := []struct {
		name string
		call func(*UseCases) error
		set  func(*automaticWorkerRepository)
	}{
		{
			name: "retry",
			call: func(service *UseCases) error { return service.RetryAutomaticTurns(t.Context(), 1) },
			set: func(repository *automaticWorkerRepository) {
				repository.retryErr = errors.New("retry candidates unavailable")
			},
		},
		{
			name: "recovery",
			call: func(service *UseCases) error { return service.RecoverAutomaticTurns(t.Context(), 1) },
			set: func(repository *automaticWorkerRepository) {
				repository.recoveryErr = errors.New("recovery candidates unavailable")
			},
		},
		{
			name: "restore",
			call: func(service *UseCases) error { return service.RestoreAutomaticTurns(t.Context(), 1) },
			set: func(repository *automaticWorkerRepository) {
				repository.restoreErr = errors.New("restore candidates unavailable")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &automaticWorkerRepository{}
			tt.set(repository)
			service := NewPersistentUseCases(repository, nil, nil, nil)
			service.ConfigureAutomaticFallback(&fallbackPlayerFake{})
			service.ConfigureAutomaticOutputRestorer(&outputRestorerFake{})
			if err := tt.call(service); err == nil {
				t.Fatal("batch operation error = nil")
			}
		})
	}
}

func TestAutomaticTurnFallbackWorkerRunsInitialPassAndStopsWhenCanceled(t *testing.T) {
	repository := &automaticWorkerRepository{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticFallback(&fallbackPlayerFake{})
	service.ConfigureAutomaticOutputRestorer(&outputRestorerFake{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	worker := NewAutomaticTurnFallbackWorker(service, 0)
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if repository.retryLimit != 20 || repository.recoveryLimit != 20 || repository.restoreLimit != 20 {
		t.Fatalf("initial pass limits = retry %d, recovery %d, restore %d; want 20", repository.retryLimit, repository.recoveryLimit, repository.restoreLimit)
	}
}

func TestAutomaticTurnFallbackWorkerRejectsMissingConfiguration(t *testing.T) {
	if err := (*AutomaticTurnFallbackWorker)(nil).Run(t.Context()); !errors.Is(err, ErrWorkerNotConfigured) {
		t.Fatalf("nil worker Run() error = %v, want ErrWorkerNotConfigured", err)
	}
	if err := NewAutomaticTurnFallbackWorker(nil, 0).Run(t.Context()); !errors.Is(err, ErrWorkerNotConfigured) {
		t.Fatalf("unconfigured worker Run() error = %v, want ErrWorkerNotConfigured", err)
	}
}

func TestAutomaticTurnFallbackWorkerRecoversTotalInitialFailureWithoutRetry(t *testing.T) {
	run := AutomaticTurnRun{
		AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1", TraceID: "trace-1",
		TargetLanguage: "zh-CN", TranslatedText: "译文", LanguageConfigVersion: 3,
		Status: AutomaticTurnRunFailed, TargetCount: 1, SettledCount: 1, FailedCount: 1,
		FallbackOperationID: "fallback_turn-1",
	}
	message := Message{ID: "message-1", AccountID: "account-1", Status: MessageStatusFailed, Attempts: 1}
	repository := &atomicScheduleRepository{
		retryRepositoryStub: retryRepositoryStub{current: map[string]Message{"account-1": message}},
		existing:            run,
		settlements: []AutomaticTurnSettlement{{
			TurnID: "turn-1", Channel: ChannelWeChat, DestinationRef: "primary-wechat",
			Status: AutomaticTurnSettlementFailed, MessageID: message.ID,
		}},
	}
	fallback := &fallbackPlayerFake{}
	restorer := &outputRestorerFake{}
	service := NewPersistentUseCases(repository, nil, nil, nil)
	service.ConfigureAutomaticFallback(fallback)
	service.ConfigureAutomaticOutputRestorer(restorer)

	if err := NewAutomaticTurnFallbackWorker(service, 0).runOnce(t.Context()); err != nil {
		t.Fatalf("runOnce() error = %v", err)
	}
	if len(repository.retried) != 0 {
		t.Fatalf("retried targets = %#v, want none", repository.retried)
	}
	if fallback.calls != 1 || !repository.fallbackPlayed {
		t.Fatalf("fallback calls=%d played=%t, want one played fallback", fallback.calls, repository.fallbackPlayed)
	}
	if !repository.restored || restorer.expectedVersion != 3 {
		t.Fatalf("restore result=%t input=%#v", repository.restored, restorer)
	}
}

type automaticWorkerRepository struct {
	retryRepositoryStub
	retryErr      error
	recoveryErr   error
	restoreErr    error
	retryLimit    int
	recoveryLimit int
	restoreLimit  int
}

func (r *automaticWorkerRepository) GetAutomaticTurnRun(context.Context, string, string) (AutomaticTurnRun, error) {
	return AutomaticTurnRun{}, nil
}

func (r *automaticWorkerRepository) ScheduleAutomaticTurn(context.Context, AutomaticTurnScheduleRecord) error {
	return nil
}

func (r *automaticWorkerRepository) ListAutomaticTurnSettlements(context.Context, string, string) ([]AutomaticTurnSettlement, error) {
	return nil, nil
}

func (r *automaticWorkerRepository) RetryAutomaticTurnTarget(context.Context, string, string, string, string) (Message, error) {
	return Message{}, nil
}

func (r *automaticWorkerRepository) ListAutomaticTurnRetryCandidates(_ context.Context, limit int) ([]AutomaticTurnRun, error) {
	r.retryLimit = limit
	return nil, r.retryErr
}

func (r *automaticWorkerRepository) ListAutomaticTurnRecoveryCandidates(_ context.Context, limit int) ([]AutomaticTurnRun, error) {
	r.recoveryLimit = limit
	return nil, r.recoveryErr
}

func (r *automaticWorkerRepository) ListAutomaticTurnRestoreCandidates(_ context.Context, limit int) ([]AutomaticTurnRun, error) {
	r.restoreLimit = limit
	return nil, r.restoreErr
}

func (r *automaticWorkerRepository) ClaimAutomaticTurnFallback(context.Context, string, string) (AutomaticTurnRun, bool, error) {
	return AutomaticTurnRun{}, false, nil
}

func (r *automaticWorkerRepository) MarkAutomaticTurnFallbackPlayed(context.Context, string, string) error {
	return nil
}

func (r *automaticWorkerRepository) MarkAutomaticTurnRestored(context.Context, string, string) error {
	return nil
}
