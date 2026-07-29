package delivery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

type workerQueueStub struct {
	mu         sync.Mutex
	items      []QueueMessage
	receiveErr error
	ackErr     error
	nackErr    error
	acks       []string
	nacks      []string
}

func (q *workerQueueStub) Enqueue(context.Context, string, string) error { return nil }

func (q *workerQueueStub) Receive(ctx context.Context) (QueueMessage, error) {
	q.mu.Lock()
	if q.receiveErr != nil {
		err := q.receiveErr
		q.mu.Unlock()
		return QueueMessage{}, err
	}
	if len(q.items) > 0 {
		item := q.items[0]
		q.items = q.items[1:]
		q.mu.Unlock()
		return item, nil
	}
	q.mu.Unlock()
	<-ctx.Done()
	return QueueMessage{}, ctx.Err()
}

func (q *workerQueueStub) Ack(_ context.Context, receipt string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.acks = append(q.acks, receipt)
	return q.ackErr
}

func (q *workerQueueStub) Nack(_ context.Context, receipt string, _ time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nacks = append(q.nacks, receipt)
	return q.nackErr
}

type workerCompletion struct {
	attemptID    string
	messageID    string
	attemptState DeliveryAttemptStatus
	messageState MessageStatus
	code         *string
}

type workerRepositoryStub struct {
	mu             sync.Mutex
	attempt        DeliveryAttempt
	message        Message
	getAttemptErr  error
	claimErr       error
	messageErr     error
	completeErr    error
	requeueErr     error
	claimResult    *DeliveryAttempt
	setAttemptErr  error
	getAttemptCall int
	claimCall      int
	readCall       int
	requeueCall    int
	setAttemptCall int
	setStates      []DeliveryAttemptStatus
	setCodes       []*string
	completions    []workerCompletion
	events         []string
}

func (r *workerRepositoryStub) CreateMessage(context.Context, CreateMessageRecord) error { return nil }

func (r *workerRepositoryStub) GetMessage(context.Context, string, string) (Message, error) {
	return r.message, nil
}

func (r *workerRepositoryStub) CreateRetry(context.Context, CreateRetryRecord) (Message, error) {
	return r.message, nil
}

func (r *workerRepositoryStub) GetAttempt(context.Context, string) (DeliveryAttempt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.getAttemptCall++
	r.events = append(r.events, "get_attempt")
	return r.attempt, r.getAttemptErr
}

func (r *workerRepositoryStub) ClaimAttempt(context.Context, string) (DeliveryAttempt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimCall++
	r.events = append(r.events, "claim_attempt")
	if r.claimErr != nil {
		return r.attempt, r.claimErr
	}
	if r.claimResult != nil {
		return *r.claimResult, nil
	}
	claimed := r.attempt
	if claimed.Status == AttemptStatusQueued {
		claimed.Status = AttemptStatusSending
	}
	r.attempt = claimed
	return claimed, nil
}

func (r *workerRepositoryStub) GetMessageForWorker(context.Context, string) (Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readCall++
	r.events = append(r.events, "get_message")
	return r.message, r.messageErr
}

func (r *workerRepositoryStub) CompleteAttempt(_ context.Context, attemptID, messageID string, attemptState DeliveryAttemptStatus, messageState MessageStatus, code *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "complete_attempt")
	copyCode := cloneString(code)
	r.completions = append(r.completions, workerCompletion{attemptID: attemptID, messageID: messageID, attemptState: attemptState, messageState: messageState, code: copyCode})
	return r.completeErr
}

func (r *workerRepositoryStub) RequeueAttempt(_ context.Context, _ string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requeueCall++
	r.events = append(r.events, "requeue_attempt")
	if r.requeueErr != nil {
		return r.requeueErr
	}
	r.attempt.Status = AttemptStatusQueued
	return nil
}

func (r *workerRepositoryStub) SetMessageStatus(context.Context, string, MessageStatus, *string) error {
	return nil
}

func (r *workerRepositoryStub) SetAttemptStatus(_ context.Context, _ string, status DeliveryAttemptStatus, code *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setAttemptCall++
	r.setStates = append(r.setStates, status)
	r.setCodes = append(r.setCodes, cloneString(code))
	r.events = append(r.events, "set_attempt")
	if r.setAttemptErr != nil {
		return r.setAttemptErr
	}
	r.attempt.Status = status
	return nil
}

func (r *workerRepositoryStub) ListPreferences(context.Context, string) ([]Preference, error) {
	return nil, nil
}

func (r *workerRepositoryStub) PutPreference(context.Context, Preference) (Preference, error) {
	return Preference{}, nil
}

type workerDestinationStub struct {
	err    error
	result *VerifiedDestination
}

func (d workerDestinationStub) ResolveVerifiedDestination(_ context.Context, accountID string, channel Channel, reference string) (VerifiedDestination, error) {
	if d.err != nil {
		return VerifiedDestination{}, d.err
	}
	if d.result != nil {
		return *d.result, nil
	}
	return VerifiedDestination{AccountID: accountID, Channel: channel, DestinationRef: reference, ProviderTarget: "target"}, nil
}

type workerProviderStub struct {
	mu         sync.Mutex
	err        error
	idempotent bool
	calls      int
	requests   []SendRequest
	events     *[]string
}

func (p *workerProviderStub) Send(_ context.Context, request SendRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.requests = append(p.requests, request)
	if p.events != nil {
		*p.events = append(*p.events, "provider_send")
	}
	return p.err
}

func (p *workerProviderStub) SupportsProviderIdempotency() bool { return p.idempotent }

func newWorkerFixture() (*workerQueueStub, *workerRepositoryStub, *workerProviderStub, *Worker) {
	queue := &workerQueueStub{}
	repository := &workerRepositoryStub{
		attempt: DeliveryAttempt{ID: "attempt-1", MessageID: "message-1", Status: AttemptStatusQueued},
		message: Message{
			ID: "message-1", AccountID: "account-1", Channel: ChannelEmail, DestinationRef: "primary",
			SnapshotVersion: 1, Status: MessageStatusQueued,
			Turns: []FinalTurnSnapshot{{TurnID: "turn-1", SessionID: "session-1", SourceLanguage: "zh-CN", TargetLanguage: "en-US", SourceText: "你好", TranslatedText: "hello"}},
		},
	}
	provider := &workerProviderStub{events: &repository.events}
	worker := NewWorker(queue, WorkerDependencies{
		Repository:   repository,
		Destinations: workerDestinationStub{},
		Provider:     provider,
	})
	return queue, repository, provider, worker
}

func TestWorkerStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := NewWorker(&workerQueueStub{})
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

func TestWorkerProcessesInOrderAndAcknowledgesAfterCommit(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if provider.calls != 1 || len(repository.completions) != 1 || len(queue.acks) != 1 || len(queue.nacks) != 0 {
		t.Fatalf("calls=%d completions=%d acks=%d nacks=%d", provider.calls, len(repository.completions), len(queue.acks), len(queue.nacks))
	}
	completion := repository.completions[0]
	if completion.attemptState != AttemptStatusSucceeded || completion.messageState != MessageStatusSent || completion.code != nil {
		t.Fatalf("completion = %#v, want succeeded/sent without error", completion)
	}
	if provider.requests[0].ProviderIdempotencyKey != "attempt-1" {
		t.Fatalf("provider key = %q, want attempt-1", provider.requests[0].ProviderIdempotencyKey)
	}
	wantEvents := []string{"get_attempt", "claim_attempt", "get_message", "provider_send", "complete_attempt"}
	if !equalStrings(repository.events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", repository.events, wantEvents)
	}
}

func TestWorkerPermanentProviderFailureCompletesAndAcknowledges(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	provider.err = fmt.Errorf("invalid target: %w", ErrProviderRejected)

	if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if provider.calls != 1 || len(repository.completions) != 1 || len(queue.acks) != 1 || len(queue.nacks) != 0 {
		t.Fatalf("calls=%d completions=%d acks=%d nacks=%d", provider.calls, len(repository.completions), len(queue.acks), len(queue.nacks))
	}
	completion := repository.completions[0]
	if completion.attemptState != AttemptStatusFailed || completion.messageState != MessageStatusFailed || completion.code == nil || *completion.code != providerFailureCode {
		t.Fatalf("completion = %#v, want failed/provider_error", completion)
	}
}

func TestWorkerTransientProviderFailureNacksWithoutTerminalWrite(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	provider.err = errors.New("provider connection reset")

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if !errors.Is(err, provider.err) {
		t.Fatalf("Process() error = %v, want provider error", err)
	}
	if provider.calls != 1 || len(repository.completions) != 0 || len(queue.acks) != 0 || len(queue.nacks) != 1 {
		t.Fatalf("calls=%d completions=%d acks=%d nacks=%d", provider.calls, len(repository.completions), len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerClaimConflictAcknowledgesQueuedDuplicate(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.claimErr = domain.ErrConflict

	if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if provider.calls != 0 || len(repository.completions) != 0 || len(queue.acks) != 1 || len(queue.nacks) != 0 {
		t.Fatalf("calls=%d completions=%d acks=%d nacks=%d", provider.calls, len(repository.completions), len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerClaimConflictNonIdempotentSendingRecordsUnknown(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.attempt.Status = AttemptStatusSending
	repository.attempt.StartedAt = timePointer(time.Now().UTC().Add(-defaultSendingLease))
	repository.claimErr = domain.ErrConflict

	if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if provider.calls != 0 || len(repository.completions) != 1 || len(queue.acks) != 1 || len(queue.nacks) != 0 {
		t.Fatalf("calls=%d completions=%d acks=%d nacks=%d", provider.calls, len(repository.completions), len(queue.acks), len(queue.nacks))
	}
	completion := repository.completions[0]
	if completion.code == nil || *completion.code != deliveryUnknownErrorCode {
		t.Fatalf("unknown code = %v, want %q", completion.code, deliveryUnknownErrorCode)
	}
}

func TestWorkerClaimConflictNonIdempotentActiveSendingNacks(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.attempt.Status = AttemptStatusSending
	repository.attempt.StartedAt = timePointer(time.Now().UTC())
	repository.claimErr = domain.ErrConflict

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if err == nil {
		t.Fatal("Process() error = nil, want active sender retry")
	}
	if provider.calls != 0 || len(repository.completions) != 0 || len(queue.acks) != 0 || len(queue.nacks) != 1 {
		t.Fatalf("calls=%d completions=%d acks=%d nacks=%d", provider.calls, len(repository.completions), len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerClaimConflictIdempotentSendingReplaysProvider(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.attempt.Status = AttemptStatusSending
	repository.claimErr = domain.ErrConflict
	provider.idempotent = true

	if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if provider.calls != 1 || len(repository.completions) != 1 || len(queue.acks) != 1 {
		t.Fatalf("calls=%d completions=%d acks=%d", provider.calls, len(repository.completions), len(queue.acks))
	}
}

func TestWorkerFailsClosedWhenClaimDoesNotReturnSending(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.claimResult = &DeliveryAttempt{ID: "attempt-1", MessageID: "message-1", Status: AttemptStatusQueued}

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if !errors.Is(err, ErrWorkerNotConfigured) {
		t.Fatalf("Process() error = %v, want claim invariant error", err)
	}
	if provider.calls != 0 || len(queue.acks) != 0 || len(queue.nacks) != 0 {
		t.Fatalf("provider calls=%d acks=%d nacks=%d, want no provider or broker settlement", provider.calls, len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerDoesNotRequeueConflictResumeBeforeProviderRead(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.attempt.Status = AttemptStatusSending
	repository.claimErr = domain.ErrConflict
	repository.messageErr = errors.New("database unavailable")
	provider.idempotent = true

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if err == nil || !errors.Is(err, repository.messageErr) {
		t.Fatalf("Process() error = %v, want message read error", err)
	}
	if repository.requeueCall != 0 || provider.calls != 0 || len(queue.acks) != 0 || len(queue.nacks) != 1 {
		t.Fatalf("requeues=%d provider=%d acks=%d nacks=%d, want 0, 0, 0, 1", repository.requeueCall, provider.calls, len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerDoesNotRequeueSuccessfulClaimFromSendingState(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.attempt.Status = AttemptStatusSending
	repository.messageErr = errors.New("database unavailable")

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if err == nil || !errors.Is(err, repository.messageErr) {
		t.Fatalf("Process() error = %v, want message read error", err)
	}
	if repository.requeueCall != 0 || provider.calls != 0 || len(queue.acks) != 0 || len(queue.nacks) != 1 {
		t.Fatalf("requeues=%d provider=%d acks=%d nacks=%d, want 0, 0, 0, 1", repository.requeueCall, provider.calls, len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerAcknowledgesPoisonQueueMessage(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()

	if err := worker.Process(t.Context(), QueueMessage{Receipt: "poison-receipt"}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if provider.calls != 0 || repository.getAttemptCall != 0 || len(queue.acks) != 1 || len(queue.nacks) != 0 {
		t.Fatalf("calls=%d reads=%d acks=%d nacks=%d", provider.calls, repository.getAttemptCall, len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerCannotSettleQueueMessageWithoutReceipt(t *testing.T) {
	_, repository, provider, worker := newWorkerFixture()
	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1"})
	if !errors.Is(err, ErrInvalidQueueMessage) {
		t.Fatalf("Process() error = %v, want invalid queue message", err)
	}
	if provider.calls != 0 || repository.getAttemptCall != 0 {
		t.Fatalf("provider calls=%d attempt reads=%d, want no processing", provider.calls, repository.getAttemptCall)
	}
}

func TestWorkerNacksRepositoryReaderDestinationAndClaimFailures(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*workerQueueStub, *workerRepositoryStub, *workerProviderStub, *Worker)
		wantReset bool
	}{
		{name: "attempt read", setup: func(_ *workerQueueStub, r *workerRepositoryStub, _ *workerProviderStub, _ *Worker) {
			r.getAttemptErr = errors.New("database unavailable")
		}},
		{name: "claim", setup: func(_ *workerQueueStub, r *workerRepositoryStub, _ *workerProviderStub, _ *Worker) {
			r.claimErr = errors.New("database unavailable")
		}},
		{name: "message read", setup: func(_ *workerQueueStub, r *workerRepositoryStub, _ *workerProviderStub, _ *Worker) {
			r.messageErr = errors.New("database unavailable")
		}, wantReset: true},
		{name: "destination", setup: func(_ *workerQueueStub, _ *workerRepositoryStub, _ *workerProviderStub, w *Worker) {
			w.deps.Destinations = workerDestinationStub{err: errors.New("destination store unavailable")}
		}, wantReset: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue, repository, provider, worker := newWorkerFixture()
			test.setup(queue, repository, provider, worker)
			err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
			if err == nil {
				t.Fatal("Process() succeeded for infrastructure failure")
			}
			if len(queue.acks) != 0 || len(queue.nacks) != 1 {
				t.Fatalf("acks=%d nacks=%d, want 0 and 1", len(queue.acks), len(queue.nacks))
			}
			if test.wantReset {
				if repository.requeueCall != 1 || repository.attempt.Status != AttemptStatusQueued {
					t.Fatalf("RequeueAttempt calls=%d status=%q, want one queued reset", repository.requeueCall, repository.attempt.Status)
				}
			} else if repository.requeueCall != 0 {
				t.Fatalf("RequeueAttempt calls=%d, want 0 before claim/provider", repository.requeueCall)
			}
		})
	}
}

func TestWorkerKeepsSendingLeaseAfterTransientProviderFailure(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	provider.err = errors.New("provider connection reset")

	if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err == nil {
		t.Fatal("Process() succeeded for transient provider error")
	}
	if repository.requeueCall != 0 {
		t.Fatalf("RequeueAttempt calls=%d, want no lease reset after provider I/O", repository.requeueCall)
	}
	if len(queue.nacks) != 1 || len(queue.acks) != 0 {
		t.Fatalf("acks=%d nacks=%d, want 0 and 1", len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerRequeuesWhenProviderIsNotConfigured(t *testing.T) {
	queue, repository, _, worker := newWorkerFixture()
	worker.deps.Provider = UnconfiguredProvider{}

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("Process() error = %v, want ErrProviderNotConfigured", err)
	}
	if repository.requeueCall != 1 || repository.attempt.Status != AttemptStatusQueued {
		t.Fatalf("requeues=%d status=%q, want one queued reset", repository.requeueCall, repository.attempt.Status)
	}
	if len(repository.completions) != 0 || len(queue.acks) != 0 || len(queue.nacks) != 1 {
		t.Fatalf("completions=%d acks=%d nacks=%d, want 0/0/1", len(repository.completions), len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerPermanentSnapshotAndDestinationFailuresCompleteAndAcknowledge(t *testing.T) {
	tests := []struct {
		name string
		set  func(*workerRepositoryStub, *Worker)
		code string
	}{
		{name: "missing snapshot", set: func(r *workerRepositoryStub, _ *Worker) { r.messageErr = domain.ErrNotFound }, code: messageFailureCode},
		{name: "revoked destination", set: func(_ *workerRepositoryStub, w *Worker) {
			w.deps.Destinations = workerDestinationStub{err: domain.ErrNotFound}
		}, code: destinationFailureCode},
		{name: "malformed snapshot", set: func(r *workerRepositoryStub, _ *Worker) { r.message.ID = "" }, code: messageFailureCode},
		{name: "empty turns", set: func(r *workerRepositoryStub, _ *Worker) { r.message.Turns = nil }, code: messageFailureCode},
		{name: "unsupported status", set: func(r *workerRepositoryStub, _ *Worker) { r.message.Status = MessageStatus("corrupt") }, code: messageFailureCode},
		{name: "mismatched destination", set: func(_ *workerRepositoryStub, w *Worker) {
			w.deps.Destinations = workerDestinationStub{result: &VerifiedDestination{AccountID: "other-account", Channel: ChannelEmail, DestinationRef: "primary", ProviderTarget: "target"}}
		}, code: destinationFailureCode},
		{name: "missing provider target", set: func(_ *workerRepositoryStub, w *Worker) {
			w.deps.Destinations = workerDestinationStub{result: &VerifiedDestination{AccountID: "account-1", Channel: ChannelEmail, DestinationRef: "primary"}}
		}, code: destinationFailureCode},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue, repository, provider, worker := newWorkerFixture()
			test.set(repository, worker)
			if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			if provider.calls != 0 || len(queue.acks) != 1 || len(queue.nacks) != 0 || len(repository.completions) != 1 {
				t.Fatalf("calls=%d acks=%d nacks=%d completions=%d", provider.calls, len(queue.acks), len(queue.nacks), len(repository.completions))
			}
			if repository.completions[0].code == nil || *repository.completions[0].code != test.code {
				t.Fatalf("completion code = %v, want %q", repository.completions[0].code, test.code)
			}
		})
	}
}

func TestWorkerReconcilesTerminalMessageBeforeAcknowledging(t *testing.T) {
	failedCode := "provider_error"
	tests := []struct {
		name          string
		messageStatus MessageStatus
		messageCode   *string
		wantAttempt   DeliveryAttemptStatus
		wantCode      *string
	}{
		{name: "sent", messageStatus: MessageStatusSent, wantAttempt: AttemptStatusSucceeded},
		{name: "failed", messageStatus: MessageStatusFailed, messageCode: &failedCode, wantAttempt: AttemptStatusFailed, wantCode: &failedCode},
		{name: "cancelled", messageStatus: MessageStatusCancelled, wantAttempt: AttemptStatusFailed, wantCode: stringPointer(messageTerminalCode)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue, repository, provider, worker := newWorkerFixture()
			repository.message.Status = test.messageStatus
			repository.message.LastErrorCode = test.messageCode
			if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			if provider.calls != 0 || len(queue.acks) != 1 || len(queue.nacks) != 0 {
				t.Fatalf("provider=%d acks=%d nacks=%d, want 0/1/0", provider.calls, len(queue.acks), len(queue.nacks))
			}
			if repository.setAttemptCall != 1 || len(repository.setStates) != 1 || repository.setStates[0] != test.wantAttempt {
				t.Fatalf("SetAttemptStatus calls=%d states=%v, want one %s", repository.setAttemptCall, repository.setStates, test.wantAttempt)
			}
			if !equalOptionalStrings(repository.setCodes, []*string{test.wantCode}) {
				t.Fatalf("attempt codes=%v, want %v", repository.setCodes, []*string{test.wantCode})
			}
		})
	}
}

func TestWorkerNacksWhenTerminalAttemptReconciliationFails(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.message.Status = MessageStatusCancelled
	repository.setAttemptErr = errors.New("database unavailable")

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if err == nil || !errors.Is(err, repository.setAttemptErr) {
		t.Fatalf("Process() error = %v, want reconciliation error", err)
	}
	if provider.calls != 0 || len(queue.acks) != 0 || len(queue.nacks) != 1 {
		t.Fatalf("provider=%d acks=%d nacks=%d, want 0/0/1", provider.calls, len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerDoesNotAcknowledgeWhenTerminalCommitFails(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	provider.err = fmt.Errorf("provider rejected: %w", ErrProviderRejected)
	repository.completeErr = errors.New("database unavailable")

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if err == nil || !errors.Is(err, repository.completeErr) {
		t.Fatalf("Process() error = %v, want terminal commit error", err)
	}
	if len(queue.acks) != 0 || len(queue.nacks) != 1 {
		t.Fatalf("acks=%d nacks=%d, want 0 and 1", len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerReturnsAckFailureWithoutNack(t *testing.T) {
	queue, _, _, worker := newWorkerFixture()
	queue.ackErr = errors.New("ack response lost")

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if err == nil || !errors.Is(err, queue.ackErr) {
		t.Fatalf("Process() error = %v, want ACK error", err)
	}
	if len(queue.acks) != 1 || len(queue.nacks) != 0 {
		t.Fatalf("acks=%d nacks=%d, want 1 and 0", len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerReturnsNackFailureAlongsideOriginalError(t *testing.T) {
	queue, repository, _, worker := newWorkerFixture()
	databaseErr := errors.New("database unavailable")
	queue.nackErr = errors.New("broker unavailable")
	repository.getAttemptErr = databaseErr

	err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"})
	if !errors.Is(err, databaseErr) || !errors.Is(err, queue.nackErr) {
		t.Fatalf("Process() error = %v, want database and NACK errors", err)
	}
	if len(queue.acks) != 0 || len(queue.nacks) != 1 {
		t.Fatalf("acks=%d nacks=%d, want 0 and 1", len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerAcknowledgesTerminalReplay(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.attempt.Status = AttemptStatusSucceeded

	if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if provider.calls != 0 || len(queue.acks) != 1 || len(queue.nacks) != 0 {
		t.Fatalf("calls=%d acks=%d nacks=%d", provider.calls, len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerAcknowledgesMissingAttempt(t *testing.T) {
	queue, repository, provider, worker := newWorkerFixture()
	repository.getAttemptErr = domain.ErrNotFound

	if err := worker.Process(t.Context(), QueueMessage{AttemptID: "attempt-1", Receipt: "receipt-1"}); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if provider.calls != 0 || len(queue.acks) != 1 || len(queue.nacks) != 0 {
		t.Fatalf("calls=%d acks=%d nacks=%d", provider.calls, len(queue.acks), len(queue.nacks))
	}
}

func TestWorkerRunStopsOnReceiveCancellation(t *testing.T) {
	queue, _, _, worker := newWorkerFixture()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(queue.acks) != 0 || len(queue.nacks) != 0 {
		t.Fatalf("acks=%d nacks=%d, want no settlement", len(queue.acks), len(queue.nacks))
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func stringPointer(value string) *string { return &value }

func timePointer(value time.Time) *time.Time { return &value }

func equalOptionalStrings(left, right []*string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if (left[index] == nil) != (right[index] == nil) {
			return false
		}
		if left[index] != nil && *left[index] != *right[index] {
			return false
		}
	}
	return true
}

var _ Repository = (*workerRepositoryStub)(nil)
var _ WorkerMessageReader = (*workerRepositoryStub)(nil)
var _ DestinationReader = workerDestinationStub{}
var _ IdempotentProvider = (*workerProviderStub)(nil)
