package turns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

// ErrAttributionSettlement marks a task Ack or Retry failure that leaves receipt state uncertain
// and requires the worker supervisor to restart the consumer.
var ErrAttributionSettlement = errors.New("attribution task settlement failed")

// maxAttributionAttempts bounds transient retries before a task is permanently failed. A task that
// reaches this limit is recorded as failed so operators can inspect and replay it explicitly.
const maxAttributionAttempts = 8

// AttributionTaskDelivery exposes the claimed task and explicit settlement control.
type AttributionTaskDelivery interface {
	Task() AttributionTask
	Ack() error
	Retry(lastError string) error
	Fail(lastError string) error
}

// AttributionTaskSource receives attributed work items with receipt-based settlement.
type AttributionTaskSource interface {
	Receive(context.Context) (AttributionTaskDelivery, error)
}

// AttributionTask identifies one durable attribution resolution request.
type AttributionTask struct {
	TaskID    string
	TurnID    string
	SessionID string
	AccountID string
	TaskType  string
	Attempts  int
}

// AttributionResolver decides the target participant for one turn. Implementations are the async
// AI/speaker-resolution boundary; a nil decision means "keep current attribution" and completes
// the task without modifying the turn.
type AttributionResolver interface {
	Resolve(ctx context.Context, input AttributionResolutionInput) (*AttributionDecision, error)
}

// AttributionOwnerReader resolves the current canonical account that owns a session. Tasks keep the
// enqueue-time account as audit data; the worker uses this reader for authorization-sensitive reads.
type AttributionOwnerReader interface {
	AccountIDForSession(ctx context.Context, sessionID string) (string, error)
}

// AttributionResolutionInput carries the persisted turn and its session participants so a
// resolver can use surrounding context without reading storage directly.
type AttributionResolutionInput struct {
	AccountID    string
	SessionID    string
	TurnID       string
	Turn         recordsv1.VoiceTurn
	Participants []recordsv1.Participant
}

// AttributionDecision is the resolver output applied through the records services. ParticipantID
// nil keeps the current turn attribution; when set it is corrected with the given status.
type AttributionDecision struct {
	ParticipantID        string
	AttributionStatus    recordsv1.AttributionStatus
	SpeakerConfidence    *float64
	SpeakerConfidenceSet bool
}

// AttributionReader reads the inputs a resolver needs, enforcing account ownership.
type AttributionReader interface {
	GetTurn(ctx context.Context, accountID, turnID string) (recordsv1.VoiceTurn, error)
	ListParticipants(ctx context.Context, accountID, sessionID string) ([]recordsv1.Participant, error)
}

// AttributionApplier persists a resolver decision through the records services.
type AttributionApplier interface {
	CorrectAttributionIfUnresolved(ctx context.Context, accountID, turnID string, request recordsv1.UpdateAttributionRequest, speakerConfidenceSet bool) (recordsv1.VoiceTurn, error)
}

// AttributionWorker drains durable attribution tasks, resolving each with the resolver and
// applying the decision through the records applier. Settlement errors restart the worker; other
// task failures are recorded as Retry or Fail and never stop unrelated tasks.
type AttributionWorker struct {
	source   AttributionTaskSource
	resolver AttributionResolver
	owners   AttributionOwnerReader
	reader   AttributionReader
	applier  AttributionApplier
	logger   *slog.Logger
}

// NewAttributionWorker validates required dependencies and returns a single-owner loop.
func NewAttributionWorker(
	source AttributionTaskSource,
	resolver AttributionResolver,
	owners AttributionOwnerReader,
	reader AttributionReader,
	applier AttributionApplier,
	logger *slog.Logger,
) (*AttributionWorker, error) {
	if source == nil || resolver == nil || owners == nil || reader == nil || applier == nil || logger == nil {
		return nil, errors.New("attribution worker dependencies are required")
	}
	return &AttributionWorker{
		source: source, resolver: resolver, owners: owners, reader: reader, applier: applier, logger: logger,
	}, nil
}

// Run drains tasks until the source fails or the context ends.
func (w *AttributionWorker) Run(ctx context.Context) error {
	for {
		delivery, err := w.source.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("receive attribution task: %w", err)
		}
		if err := w.handle(ctx, delivery); err != nil {
			if errors.Is(err, ErrAttributionSettlement) {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
		}
	}
}

// Process handles one already-claimed delivery, enabling deterministic one-step tests.
func (w *AttributionWorker) Process(ctx context.Context, delivery AttributionTaskDelivery) error {
	return w.handle(ctx, delivery)
}

func (w *AttributionWorker) handle(ctx context.Context, delivery AttributionTaskDelivery) error {
	task := delivery.Task()
	accountID, err := w.owners.AccountIDForSession(ctx, task.SessionID)
	if err != nil {
		return w.settleFailure(delivery, task, fmt.Errorf("resolve owner for session %s: %w", task.SessionID, err))
	}
	decision, err := w.resolve(ctx, task, accountID)
	if err != nil {
		return w.settleFailure(delivery, task, err)
	}
	if decision == nil || decision.ParticipantID == "" {
		if err := delivery.Ack(); err != nil {
			return fmt.Errorf("%w: ack attribution task: %w", ErrAttributionSettlement, err)
		}
		return nil
	}
	if _, err := w.applier.CorrectAttributionIfUnresolved(ctx, accountID, task.TurnID, recordsv1.UpdateAttributionRequest{
		ParticipantID:     decision.ParticipantID,
		AttributionStatus: decision.AttributionStatus,
		SpeakerConfidence: decision.SpeakerConfidence,
	}, decision.SpeakerConfidenceSet); err != nil {
		if errors.Is(err, ErrStaleAttribution) {
			if ackErr := delivery.Ack(); ackErr != nil {
				return fmt.Errorf("%w: ack stale attribution task: %w", ErrAttributionSettlement, errors.Join(err, ackErr))
			}
			return nil
		}
		return w.settleFailure(delivery, task, fmt.Errorf("apply attribution decision: %w", err))
	}
	if err := delivery.Ack(); err != nil {
		return fmt.Errorf("%w: ack attribution task: %w", ErrAttributionSettlement, err)
	}
	return nil
}

// settleFailure fails the task permanently when the cause is not retryable or the attempt limit is
// reached, otherwise schedules a retry. Settlement errors still fail the worker supervisor.
func (w *AttributionWorker) settleFailure(delivery AttributionTaskDelivery, task AttributionTask, cause error) error {
	if errors.Is(cause, ErrAttributionNoEvidence) || task.Attempts >= maxAttributionAttempts {
		w.logger.Error("attribution task failed permanently",
			"task_id", task.TaskID, "turn_id", task.TurnID, "attempt", task.Attempts, "error", cause)
		if err := delivery.Fail(cause.Error()); err != nil {
			return fmt.Errorf("%w: fail attribution task: %w", ErrAttributionSettlement, errors.Join(cause, err))
		}
		return nil
	}
	return w.retry(delivery, task, cause)
}

func (w *AttributionWorker) resolve(ctx context.Context, task AttributionTask, accountID string) (*AttributionDecision, error) {
	turn, err := w.reader.GetTurn(ctx, accountID, task.TurnID)
	if err != nil {
		return nil, fmt.Errorf("read turn %s: %w", task.TurnID, err)
	}
	participants, err := w.reader.ListParticipants(ctx, accountID, task.SessionID)
	if err != nil {
		return nil, fmt.Errorf("list participants for turn %s: %w", task.TurnID, err)
	}
	decision, err := w.resolver.Resolve(ctx, AttributionResolutionInput{
		AccountID:    accountID,
		SessionID:    task.SessionID,
		TurnID:       task.TurnID,
		Turn:         turn,
		Participants: participants,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve attribution for turn %s: %w", task.TurnID, err)
	}
	return decision, nil
}

func (w *AttributionWorker) retry(delivery AttributionTaskDelivery, task AttributionTask, cause error) error {
	w.logger.WarnContext(context.Background(), "attribution task failed; scheduling retry",
		"task_id", task.TaskID, "turn_id", task.TurnID, "attempt", task.Attempts, "error", cause)
	if err := delivery.Retry(cause.Error()); err != nil {
		return fmt.Errorf("%w: retry attribution task: %w", ErrAttributionSettlement, errors.Join(cause, err))
	}
	return nil
}
