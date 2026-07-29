package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

var (
	// ErrWorkerNotConfigured means that a worker was created without all of its
	// durable delivery dependencies. A partially wired worker must never consume
	// a broker message because doing so would lose the retry opportunity.
	ErrWorkerNotConfigured = errors.New("delivery worker not configured")
	// ErrInvalidQueueMessage identifies a broker item that cannot be correlated
	// with a durable attempt. Such an item is poison and is acknowledged when a
	// broker receipt is available so it cannot block the consumer group forever.
	ErrInvalidQueueMessage = errors.New("invalid delivery queue message")
	// ErrDeliveryUnknown is used when a non-idempotent provider may have accepted
	// a request before the worker lost its result. It deliberately maps to the
	// stable delivery_unknown database error code.
	ErrDeliveryUnknown = errors.New(deliveryUnknownErrorCode)
)

const (
	defaultWorkerNackDelay = time.Second
	providerFailureCode    = "provider_error"
	messageFailureCode     = "message_snapshot_invalid"
	destinationFailureCode = "destination_unavailable"
	messageTerminalCode    = "message_terminal"
)

// WorkerDependencies are the durable and external boundaries required to
// process one delivery attempt. Repository must also implement
// WorkerMessageReader; that optional reader keeps the public Repository port
// usable by request-only adapters.
type WorkerDependencies struct {
	Repository   Repository
	Destinations DestinationReader
	Provider     Provider
}

// Worker owns one cancellable broker-consumer loop. It performs the durable
// claim before resolving a destination or calling a provider, and acknowledges
// a receipt only after the corresponding terminal repository transition is
// committed.
type Worker struct {
	queue    Queue
	deps     WorkerDependencies
	legacy   bool
	nackWait time.Duration
}

// NewWorker binds the queue and, optionally, the dependencies needed to run
// delivery. The variadic form preserves the original one-argument constructor
// for callers that only needed a cancellable lifecycle placeholder. Such a
// legacy worker remains inert until its context is cancelled; a configured
// worker fails closed immediately when a dependency is missing.
func NewWorker(queue Queue, dependencies ...WorkerDependencies) *Worker {
	worker := &Worker{queue: queue, nackWait: defaultWorkerNackDelay, legacy: len(dependencies) == 0}
	if len(dependencies) > 0 {
		worker.deps = dependencies[0]
	}
	return worker
}

// NewConfiguredWorker is an explicit spelling for production wiring. NewWorker
// remains available for compatibility with the original queue-only contract.
func NewConfiguredWorker(queue Queue, dependencies WorkerDependencies) *Worker {
	return NewWorker(queue, dependencies)
}

// Run consumes one item at a time. Process returns after a transient failure
// (the receipt has already been NACKed), allowing a supervisor to restart the
// loop rather than silently continuing with a potentially unhealthy database or
// broker connection.
func (w *Worker) Run(ctx context.Context) error {
	if ctx == nil {
		return ErrWorkerNotConfigured
	}
	if !w.ready() {
		if w != nil && w.legacy {
			<-ctx.Done()
			return nil
		}
		return ErrWorkerNotConfigured
	}
	for {
		item, err := w.queue.Receive(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return fmt.Errorf("receive delivery attempt: %w", err)
		}
		if err := w.Process(ctx, item); err != nil {
			// During shutdown the in-flight provider/database call may return a
			// cancellation after the broker receipt was delivered. Leave that
			// receipt pending for recovery; attempting ACK/NACK with the cancelled
			// context cannot improve delivery safety.
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// Process settles one broker receipt. The order is intentional: a successful
// ClaimAttempt is the ownership proof that permits provider I/O; terminal state
// is committed before ACK; every non-terminal failure is NACKed so another
// worker can retry it.
func (w *Worker) Process(ctx context.Context, item QueueMessage) error {
	if ctx == nil {
		return ErrWorkerNotConfigured
	}
	if !w.ready() {
		return ErrWorkerNotConfigured
	}
	if item.AttemptID == "" {
		return w.settleInvalid(ctx, item, fmt.Errorf("%w: attempt id is empty", ErrInvalidQueueMessage))
	}
	if item.Receipt == "" {
		// There is no broker handle with which to settle this item. Returning the
		// validation error is safer than pretending it was acknowledged.
		return fmt.Errorf("%w: receipt is empty", ErrInvalidQueueMessage)
	}

	reader, ok := w.deps.Repository.(WorkerMessageReader)
	if !ok {
		return w.retry(ctx, item, ErrWorkerNotConfigured)
	}
	existing, err := w.deps.Repository.GetAttempt(ctx, item.AttemptID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// The database is authoritative. A deleted attempt cannot ever be
			// completed, so discard this stale broker entry.
			return w.ack(ctx, item)
		}
		return w.retry(ctx, item, fmt.Errorf("read delivery attempt: %w", err))
	}
	if existing.ID == "" || existing.ID != item.AttemptID || existing.MessageID == "" {
		return w.settleInvalid(ctx, item, fmt.Errorf("%w: attempt identity is incomplete or mismatched", ErrInvalidQueueMessage))
	}

	switch existing.Status {
	case AttemptStatusSucceeded, AttemptStatusFailed:
		// Redelivery after a committed terminal transition is harmless.
		return w.ack(ctx, item)
	case AttemptStatusQueued, AttemptStatusSending:
		// Continue to the atomic claim below. A sending attempt can only be
		// resumed after ClaimAttempt reports the expected conflict.
	default:
		return w.settleInvalid(ctx, item, fmt.Errorf("%w: unsupported attempt status %q", ErrInvalidQueueMessage, existing.Status))
	}

	attempt, err := w.deps.Repository.ClaimAttempt(ctx, item.AttemptID)
	if errors.Is(err, domain.ErrNotFound) {
		return w.ack(ctx, item)
	}
	if errors.Is(err, domain.ErrConflict) {
		return w.handleClaimConflict(ctx, item, reader, existing)
	}
	if err != nil {
		return w.retry(ctx, item, fmt.Errorf("claim delivery attempt: %w", err))
	}
	if attempt.ID == "" || attempt.ID != item.AttemptID || attempt.MessageID == "" || attempt.MessageID != existing.MessageID {
		return w.settleInvalid(ctx, item, fmt.Errorf("%w: claimed attempt identity is incomplete or mismatched", ErrInvalidQueueMessage))
	}
	if attempt.Status == AttemptStatusSucceeded || attempt.Status == AttemptStatusFailed {
		return w.ack(ctx, item)
	}
	if attempt.Status != AttemptStatusSending {
		// A successful claim must return the sending state. There is no safe
		// broker settlement if a repository violates that ownership contract:
		// ACK could lose queued work, while NACK would create an unbounded poison
		// loop. Leave the receipt pending and fail the worker so the invariant is
		// visible to the supervisor and an operator can repair the adapter.
		return fmt.Errorf("%w: claim returned status %q", ErrWorkerNotConfigured, attempt.Status)
	}
	// A successful ClaimAttempt normally starts from queued. Keep the original
	// state in the preparation-recovery decision: an adapter that returns a
	// sending row for an already-sending attempt must not make a pre-provider
	// error look safe to requeue, because the provider may already have been
	// invoked by another worker.
	return w.deliver(ctx, item, reader, attempt, existing.Status == AttemptStatusQueued)
}

func (w *Worker) handleClaimConflict(ctx context.Context, item QueueMessage, reader WorkerMessageReader, existing DeliveryAttempt) error {
	switch existing.Status {
	case AttemptStatusSucceeded, AttemptStatusFailed:
		return w.ack(ctx, item)
	case AttemptStatusSending:
		if !providerSupportsIdempotency(w.deps.Provider) {
			code := deliveryUnknownErrorCode
			if err := w.deps.Repository.CompleteAttempt(ctx, existing.ID, existing.MessageID, AttemptStatusFailed, MessageStatusFailed, &code); err != nil {
				return w.retry(ctx, item, fmt.Errorf("record unknown delivery outcome: %w", err))
			}
			return w.ack(ctx, item)
		}
		// The provider promises to deduplicate the durable attempt ID, so a
		// sending row left by a crashed worker can safely resume.
		return w.deliver(ctx, item, reader, existing, false)
	case AttemptStatusQueued:
		// Another worker won the atomic claim race. It owns the provider call;
		// this receipt is a duplicate and can be settled without sending twice.
		return w.ack(ctx, item)
	default:
		return w.settleInvalid(ctx, item, fmt.Errorf("%w: claim conflict in status %q", ErrInvalidQueueMessage, existing.Status))
	}
}

func (w *Worker) deliver(ctx context.Context, item QueueMessage, reader WorkerMessageReader, attempt DeliveryAttempt, freshClaim bool) error {
	message, err := reader.GetMessageForWorker(ctx, attempt.MessageID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) && attempt.MessageID != "" {
			return w.completeFailedAndAck(ctx, item, attempt.ID, attempt.MessageID, messageFailureCode, fmt.Errorf("read delivery message: %w", err))
		}
		return w.retryMessagePreparation(ctx, item, attempt, freshClaim, fmt.Errorf("read delivery message: %w", err))
	}
	if message.ID != attempt.MessageID || !validMessageSnapshot(message) {
		return w.completeFailedAndAck(ctx, item, attempt.ID, attempt.MessageID, messageFailureCode, fmt.Errorf("%w: message snapshot identity or channel is invalid", ErrInvalidQueueMessage))
	}
	switch message.Status {
	case MessageStatusSent, MessageStatusFailed, MessageStatusCancelled:
		// A terminal message can still have a stale broker entry after a
		// concurrent retry or cancellation. Its durable state is authoritative;
		// do not invoke a provider for an entry that is no longer eligible. Reconcile
		// the attempt before ACK so a successful claim cannot leave an orphaned
		// sending row behind.
		return w.reconcileTerminalMessage(ctx, item, attempt, message)
	case MessageStatusQueued, MessageStatusRetrying, MessageStatusSending:
		// Eligible states continue through destination resolution.
	default:
		return w.completeFailedAndAck(ctx, item, attempt.ID, attempt.MessageID, messageFailureCode, fmt.Errorf("%w: unsupported message status %q", ErrInvalidQueueMessage, message.Status))
	}
	destination, err := w.deps.Destinations.ResolveVerifiedDestination(ctx, message.AccountID, message.Channel, message.DestinationRef)
	if err != nil {
		if isPermanentDestinationError(err) {
			return w.completeFailedAndAck(ctx, item, attempt.ID, message.ID, destinationFailureCode, fmt.Errorf("resolve verified destination: %w", err))
		}
		return w.retryMessagePreparation(ctx, item, attempt, freshClaim, fmt.Errorf("resolve verified destination: %w", err))
	}
	if destination.AccountID != message.AccountID || destination.Channel != message.Channel || destination.DestinationRef != message.DestinationRef || strings.TrimSpace(destination.ProviderTarget) == "" {
		return w.completeFailedAndAck(ctx, item, attempt.ID, message.ID, destinationFailureCode, fmt.Errorf("%w: verified destination identity is invalid", ErrInvalidQueueMessage))
	}

	sendErr := w.deps.Provider.Send(ctx, SendRequest{
		Message:                message,
		Attempt:                attempt,
		Destination:            destination,
		ProviderIdempotencyKey: attempt.ID,
	})
	if sendErr != nil {
		if errors.Is(sendErr, ErrDeliveryUnknown) {
			code := deliveryUnknownErrorCode
			if err := w.deps.Repository.CompleteAttempt(ctx, attempt.ID, message.ID, AttemptStatusFailed, MessageStatusFailed, &code); err != nil {
				return w.retry(ctx, item, fmt.Errorf("record unknown provider outcome: %w", err))
			}
			return w.ack(ctx, item)
		}
		if isPermanentProviderError(sendErr) {
			code := providerFailureCode
			if err := w.deps.Repository.CompleteAttempt(ctx, attempt.ID, message.ID, AttemptStatusFailed, MessageStatusFailed, &code); err != nil {
				return w.retry(ctx, item, fmt.Errorf("record provider failure: %w", err))
			}
			return w.ack(ctx, item)
		}
		// A network timeout or context cancellation leaves provider acceptance
		// unknown. Keep the sending row and NACK; a later worker either safely
		// replays an idempotent provider or records delivery_unknown.
		return w.retry(ctx, item, fmt.Errorf("send delivery: %w", sendErr))
	}
	if err := w.deps.Repository.CompleteAttempt(ctx, attempt.ID, message.ID, AttemptStatusSucceeded, MessageStatusSent, nil); err != nil {
		return w.retry(ctx, item, fmt.Errorf("record successful delivery: %w", err))
	}
	return w.ack(ctx, item)
}

func (w *Worker) reconcileTerminalMessage(ctx context.Context, item QueueMessage, attempt DeliveryAttempt, message Message) error {
	status := AttemptStatusFailed
	code := message.LastErrorCode
	if message.Status == MessageStatusSent {
		status = AttemptStatusSucceeded
		code = nil
	} else if code == nil || strings.TrimSpace(*code) == "" {
		terminalCode := messageTerminalCode
		code = &terminalCode
	}
	if err := w.deps.Repository.SetAttemptStatus(ctx, attempt.ID, status, code); err != nil {
		return w.retry(ctx, item, fmt.Errorf("reconcile terminal delivery attempt: %w", err))
	}
	return w.ack(ctx, item)
}

func (w *Worker) settleInvalid(ctx context.Context, item QueueMessage, cause error) error {
	if item.Receipt == "" {
		return cause
	}
	if err := w.queue.Ack(ctx, item.Receipt); err != nil {
		return errors.Join(cause, fmt.Errorf("ack invalid delivery: %w", err))
	}
	return nil
}

func (w *Worker) completeFailedAndAck(ctx context.Context, item QueueMessage, attemptID, messageID, code string, cause error) error {
	if attemptID == "" || messageID == "" {
		return w.retry(ctx, item, cause)
	}
	if err := w.deps.Repository.CompleteAttempt(ctx, attemptID, messageID, AttemptStatusFailed, MessageStatusFailed, &code); err != nil {
		return w.retry(ctx, item, errors.Join(cause, fmt.Errorf("record terminal delivery failure: %w", err)))
	}
	if err := w.ack(ctx, item); err != nil {
		return errors.Join(cause, err)
	}
	return nil
}

func (w *Worker) ack(ctx context.Context, item QueueMessage) error {
	if err := w.queue.Ack(ctx, item.Receipt); err != nil {
		// An ACK response can be lost after the broker accepts it. Do not NACK
		// here: that would turn an ambiguous acknowledgement into a duplicate
		// provider invocation.
		return fmt.Errorf("ack delivery: %w", err)
	}
	return nil
}

func (w *Worker) retry(ctx context.Context, item QueueMessage, cause error) error {
	if item.Receipt == "" {
		return cause
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(cause, ctxErr)
	}
	delay := w.nackWait
	if delay <= 0 {
		delay = defaultWorkerNackDelay
	}
	if err := w.queue.Nack(ctx, item.Receipt, time.Now().Add(delay)); err != nil {
		return errors.Join(cause, fmt.Errorf("nack delivery: %w", err))
	}
	return cause
}

// retryBeforeProvider releases the sending lease when an error occurs before
// provider I/O. Without this transition, a non-idempotent redelivery would
// conservatively become delivery_unknown even though no outbound request was
// made. Provider errors intentionally use retry directly because acceptance is
// then ambiguous.
func (w *Worker) retryBeforeProvider(ctx context.Context, item QueueMessage, attempt DeliveryAttempt, cause error) error {
	if attempt.ID != "" {
		nextAttemptAt := time.Now().UTC().Add(w.retryDelay())
		if err := w.deps.Repository.RequeueAttempt(ctx, attempt.ID, nextAttemptAt); err != nil {
			cause = errors.Join(cause, fmt.Errorf("requeue delivery attempt: %w", err))
		}
	}
	return w.retry(ctx, item, cause)
}

func (w *Worker) retryDelay() time.Duration {
	if w == nil || w.nackWait <= 0 {
		return defaultWorkerNackDelay
	}
	return w.nackWait
}

func (w *Worker) retryMessagePreparation(ctx context.Context, item QueueMessage, attempt DeliveryAttempt, freshClaim bool, cause error) error {
	if !freshClaim {
		// A sending row recovered after a crash may already have reached the
		// provider. Keep its lease and let the idempotency/unknown outcome path
		// decide what a later redelivery may do.
		return w.retry(ctx, item, cause)
	}
	return w.retryBeforeProvider(ctx, item, attempt, cause)
}

func (w *Worker) ready() bool {
	if w == nil || w.queue == nil {
		return false
	}
	if w.deps.Repository == nil || w.deps.Destinations == nil || w.deps.Provider == nil {
		return false
	}
	_, ok := w.deps.Repository.(WorkerMessageReader)
	return ok
}

func providerSupportsIdempotency(provider Provider) bool {
	capable, ok := provider.(IdempotentProvider)
	return ok && capable.SupportsProviderIdempotency()
}

func validMessageSnapshot(message Message) bool {
	if message.AccountID == "" || message.DestinationRef == "" || !IsSupportedChannel(message.Channel) || message.SnapshotVersion < 1 || len(message.Turns) == 0 {
		return false
	}
	for _, turn := range message.Turns {
		if strings.TrimSpace(turn.TurnID) == "" || strings.TrimSpace(turn.SessionID) == "" || strings.TrimSpace(turn.SourceLanguage) == "" || strings.TrimSpace(turn.TargetLanguage) == "" || strings.TrimSpace(turn.SourceText) == "" || strings.TrimSpace(turn.TranslatedText) == "" {
			return false
		}
	}
	return true
}

func isPermanentProviderError(err error) bool {
	return errors.Is(err, ErrProviderRejected) || errors.Is(err, domain.ErrNotImplemented) || errors.Is(err, domain.ErrInvalidArgument) || errors.Is(err, domain.ErrUnauthorized) || errors.Is(err, domain.ErrForbidden)
}

func isPermanentDestinationError(err error) bool {
	return errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrInvalidArgument) || errors.Is(err, domain.ErrUnauthorized) || errors.Is(err, domain.ErrForbidden)
}

// RetryAfterFailure is retained for dispatchers that choose delayed broker
// redelivery outside the worker's Process method.
func RetryAfterFailure(ctx context.Context, queue Queue, item QueueMessage, attempt int) error {
	if queue == nil {
		return ErrWorkerNotConfigured
	}
	if attempt < 1 {
		attempt = 1
	}
	// Cap the multiplication to keep a malformed attempt counter from wrapping
	// into a negative duration and causing an immediate hot loop.
	if attempt > 60 {
		attempt = 60
	}
	delay := time.Duration(attempt*attempt) * time.Second
	return queue.Nack(ctx, item.Receipt, time.Now().Add(delay))
}
