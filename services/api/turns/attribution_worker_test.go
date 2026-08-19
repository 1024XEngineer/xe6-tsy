package turns

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestAttributionWorkerAppliesDecisionAndAcks(t *testing.T) {
	applier := &attributionApplierStub{updated: recordsv1.VoiceTurn{ID: "vt_01"}}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{&attributionDeliveryStub{task: taskFixture()}}},
		&fixedDecisionResolver{decision: &AttributionDecision{
			ParticipantID:        "p_02",
			AttributionStatus:    recordsv1.AttributionCorrected,
			SpeakerConfidence:    ptrFloat64(0.91),
			SpeakerConfidenceSet: true,
		}},
		applier,
	)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(applier.calls) != 1 || applier.calls[0].ParticipantID != "p_02" {
		t.Fatalf("applier calls = %#v, want one correction to p_02", applier.calls)
	}
}

func TestAttributionWorkerAcksNoDecisionWithoutMutation(t *testing.T) {
	applier := &attributionApplierStub{}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{&attributionDeliveryStub{task: taskFixture()}}},
		&fixedDecisionResolver{},
		applier,
	)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(applier.calls) != 0 {
		t.Fatalf("applier calls = %d, want 0 for no decision", len(applier.calls))
	}
}

func TestAttributionWorkerRetriesResolutionError(t *testing.T) {
	delivery := &attributionDeliveryStub{task: taskFixture()}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{err: errors.New("resolver unavailable")},
		&attributionApplierStub{},
	)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !delivery.retried {
		t.Fatal("task was not retried after resolver error")
	}
	if delivery.acked {
		t.Fatal("task was acked after resolver error")
	}
}

func TestAttributionWorkerFailsPermanentlyOnNoEvidence(t *testing.T) {
	delivery := &attributionDeliveryStub{task: taskFixture()}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{err: fmt.Errorf("%w: no key", ErrAttributionNoEvidence)},
		&attributionApplierStub{},
	)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !delivery.failed {
		t.Fatal("task was not failed on permanent no-evidence error")
	}
	if delivery.retried || delivery.acked {
		t.Fatal("permanent failure must neither retry nor ack")
	}
}

func TestAttributionWorkerFailsWhenAttemptLimitReached(t *testing.T) {
	delivery := &attributionDeliveryStub{task: taskFixture()}
	delivery.task.Attempts = maxAttributionAttempts
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{err: errors.New("transient resolver outage")},
		&attributionApplierStub{},
	)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !delivery.failed {
		t.Fatal("task was not failed when the attempt limit was reached")
	}
	if delivery.retried {
		t.Fatal("task must not retry past the attempt limit")
	}
}

func TestAttributionWorkerRetriesApplyError(t *testing.T) {
	delivery := &attributionDeliveryStub{task: taskFixture()}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{decision: &AttributionDecision{ParticipantID: "p_02", AttributionStatus: recordsv1.AttributionConfirmed}},
		&attributionApplierStub{err: errors.New("apply failed")},
	)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !delivery.retried {
		t.Fatal("task was not retried after apply error")
	}
}

func TestAttributionWorkerUsesCurrentSessionOwner(t *testing.T) {
	delivery := &attributionDeliveryStub{task: AttributionTask{
		TaskID: "attr_vt_01", TurnID: "vt_01", SessionID: "vs_01", AccountID: "acct_old",
		TaskType: "turn_attribution", Attempts: 1,
	}}
	reader := &attributionReaderStub{
		turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"},
	}
	applier := &attributionApplierStub{updated: recordsv1.VoiceTurn{ID: "vt_01"}}
	worker, err := NewAttributionWorker(
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{decision: &AttributionDecision{ParticipantID: "p_02", AttributionStatus: recordsv1.AttributionConfirmed}},
		attributionOwnerStub{accountID: "acct_new"},
		reader,
		applier,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewAttributionWorker() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	if err := worker.Process(ctx, delivery); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if reader.getAccountID != "acct_new" {
		t.Fatalf("reader account ID = %q, want acct_new", reader.getAccountID)
	}
	if applier.accountID != "acct_new" {
		t.Fatalf("applier account ID = %q, want acct_new", applier.accountID)
	}
}

func TestAttributionWorkerAcksStaleDecision(t *testing.T) {
	delivery := &attributionDeliveryStub{task: taskFixture()}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{decision: &AttributionDecision{ParticipantID: "p_02", AttributionStatus: recordsv1.AttributionConfirmed}},
		&attributionApplierStub{err: ErrStaleAttribution},
	)

	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !delivery.acked {
		t.Fatal("stale attribution task was not acked")
	}
	if delivery.retried || delivery.failed {
		t.Fatal("stale attribution task must not retry or fail")
	}
}

func TestAttributionWorkerStopsOnSettlementFailure(t *testing.T) {
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{&attributionDeliveryStub{
			task:   taskFixture(),
			ackErr: errors.New("ack unavailable"),
		}}},
		&fixedDecisionResolver{},
		&attributionApplierStub{},
	)

	err := worker.Run(ctx)
	if !errors.Is(err, ErrAttributionSettlement) {
		t.Fatalf("Run() error = %v, want settlement error", err)
	}
}

func TestAttributionWorkerPreservesAckFailure(t *testing.T) {
	ackErr := errors.New("ack store unavailable")
	delivery := &attributionDeliveryStub{task: taskFixture(), ackErr: ackErr}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{},
		&attributionApplierStub{},
	)

	err := worker.Run(ctx)
	if !errors.Is(err, ErrAttributionSettlement) {
		t.Fatalf("Run() error = %v, want settlement error", err)
	}
	if !errors.Is(err, ackErr) {
		t.Fatalf("Run() error = %v, want underlying ack error preserved", err)
	}
}

func TestAttributionWorkerPreservesRetryFailure(t *testing.T) {
	retryErr := errors.New("retry store unavailable")
	cause := errors.New("resolver outage")
	delivery := &attributionDeliveryStub{task: taskFixture(), retryErr: retryErr}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{err: cause},
		&attributionApplierStub{},
	)

	err := worker.Run(ctx)
	if !errors.Is(err, ErrAttributionSettlement) {
		t.Fatalf("Run() error = %v, want settlement error", err)
	}
	if !errors.Is(err, cause) || !errors.Is(err, retryErr) {
		t.Fatalf("Run() error = %v, want cause and retry error preserved", err)
	}
}

func TestAttributionWorkerPreservesFailFailure(t *testing.T) {
	failErr := errors.New("fail store unavailable")
	cause := errors.New("permanent outage")
	delivery := &attributionDeliveryStub{task: taskFixture(), failErr: failErr}
	delivery.task.Attempts = maxAttributionAttempts
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{err: cause},
		&attributionApplierStub{},
	)

	err := worker.Run(ctx)
	if !errors.Is(err, ErrAttributionSettlement) {
		t.Fatalf("Run() error = %v, want settlement error", err)
	}
	if !errors.Is(err, cause) || !errors.Is(err, failErr) {
		t.Fatalf("Run() error = %v, want cause and fail error preserved", err)
	}
}

func TestAttributionWorkerPreservesStaleAckFailure(t *testing.T) {
	ackErr := errors.New("ack store unavailable")
	delivery := &attributionDeliveryStub{task: taskFixture(), ackErr: ackErr}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{delivery}},
		&fixedDecisionResolver{decision: &AttributionDecision{ParticipantID: "p_02", AttributionStatus: recordsv1.AttributionConfirmed}},
		&attributionApplierStub{err: ErrStaleAttribution},
	)

	err := worker.Run(ctx)
	if !errors.Is(err, ErrAttributionSettlement) {
		t.Fatalf("Run() error = %v, want settlement error", err)
	}
	if !errors.Is(err, ErrStaleAttribution) || !errors.Is(err, ackErr) {
		t.Fatalf("Run() error = %v, want stale and ack errors preserved", err)
	}
}

func TestNewAttributionWorkerValidatesDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name     string
		source   AttributionTaskSource
		resolver AttributionResolver
		owners   AttributionOwnerReader
		reader   AttributionReader
		applier  AttributionApplier
		logger   *slog.Logger
		wantErr  bool
	}{
		{name: "nil source", source: nil, resolver: &fixedDecisionResolver{}, owners: attributionOwnerStub{accountID: "acct_01"}, reader: &attributionReaderStub{}, applier: &attributionApplierStub{}, logger: logger, wantErr: true},
		{name: "nil resolver", source: &attributionSourceStub{}, resolver: nil, owners: attributionOwnerStub{accountID: "acct_01"}, reader: &attributionReaderStub{}, applier: &attributionApplierStub{}, logger: logger, wantErr: true},
		{name: "nil owners", source: &attributionSourceStub{}, resolver: &fixedDecisionResolver{}, owners: nil, reader: &attributionReaderStub{}, applier: &attributionApplierStub{}, logger: logger, wantErr: true},
		{name: "nil reader", source: &attributionSourceStub{}, resolver: &fixedDecisionResolver{}, owners: attributionOwnerStub{accountID: "acct_01"}, reader: nil, applier: &attributionApplierStub{}, logger: logger, wantErr: true},
		{name: "nil applier", source: &attributionSourceStub{}, resolver: &fixedDecisionResolver{}, owners: attributionOwnerStub{accountID: "acct_01"}, reader: &attributionReaderStub{}, applier: nil, logger: logger, wantErr: true},
		{name: "nil logger", source: &attributionSourceStub{}, resolver: &fixedDecisionResolver{}, owners: attributionOwnerStub{accountID: "acct_01"}, reader: &attributionReaderStub{}, applier: &attributionApplierStub{}, logger: nil, wantErr: true},
		{name: "all dependencies", source: &attributionSourceStub{}, resolver: &fixedDecisionResolver{}, owners: attributionOwnerStub{accountID: "acct_01"}, reader: &attributionReaderStub{}, applier: &attributionApplierStub{}, logger: logger},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			worker, err := NewAttributionWorker(test.source, test.resolver, test.owners, test.reader, test.applier, test.logger)
			if test.wantErr {
				if err == nil {
					t.Fatal("NewAttributionWorker() error = nil, want dependency error")
				}
				if !strings.Contains(err.Error(), "dependencies are required") {
					t.Fatalf("NewAttributionWorker() error = %v, want dependency error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewAttributionWorker() error = %v", err)
			}
			if worker == nil || worker.source == nil || worker.resolver == nil || worker.owners == nil || worker.reader == nil || worker.applier == nil || worker.logger == nil {
				t.Fatalf("NewAttributionWorker() = %#v, want all dependencies wired", worker)
			}
		})
	}
}

func newAttributionWorkerStub(t *testing.T, source AttributionTaskSource, resolver AttributionResolver, applier AttributionApplier) (*AttributionWorker, context.Context) {
	t.Helper()
	reader := &attributionReaderStub{
		turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"},
	}
	worker, err := NewAttributionWorker(source, resolver, attributionOwnerStub{accountID: "acct_01"}, reader, applier, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAttributionWorker() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	if stubbed, ok := source.(*attributionSourceStub); ok {
		stubbed.cancel = cancel
	}
	return worker, ctx
}

func taskFixture() AttributionTask {
	return AttributionTask{
		TaskID: "attr_vt_01", TurnID: "vt_01", SessionID: "vs_01", AccountID: "acct_01",
		TaskType: "turn_attribution", Attempts: 1,
	}
}

type attributionSourceStub struct {
	deliveries []AttributionTaskDelivery
	cancel     context.CancelFunc
}

func (s *attributionSourceStub) Receive(ctx context.Context) (AttributionTaskDelivery, error) {
	if len(s.deliveries) == 0 {
		s.cancel()
		return nil, context.Canceled
	}
	delivery := s.deliveries[0]
	s.deliveries = s.deliveries[1:]
	return delivery, nil
}

type attributionDeliveryStub struct {
	task      AttributionTask
	acked     bool
	retried   bool
	failed    bool
	ackErr    error
	retryErr  error
	failErr   error
	retryLast string
	failLast  string
}

func (d *attributionDeliveryStub) Task() AttributionTask { return d.task }
func (d *attributionDeliveryStub) Ack() error {
	if d.ackErr != nil {
		return d.ackErr
	}
	d.acked = true
	return nil
}
func (d *attributionDeliveryStub) Retry(lastError string) error {
	d.retried = true
	d.retryLast = lastError
	return d.retryErr
}
func (d *attributionDeliveryStub) Fail(lastError string) error {
	d.failed = true
	d.failLast = lastError
	return d.failErr
}

type fixedDecisionResolver struct {
	decision *AttributionDecision
	err      error
}

func (r *fixedDecisionResolver) Resolve(context.Context, AttributionResolutionInput) (*AttributionDecision, error) {
	return r.decision, r.err
}

type attributionReaderStub struct {
	turn         recordsv1.VoiceTurn
	err          error
	getAccountID string
}

func (r *attributionReaderStub) GetTurn(_ context.Context, accountID string, _ string) (recordsv1.VoiceTurn, error) {
	r.getAccountID = accountID
	return r.turn, r.err
}

type attributionOwnerStub struct {
	accountID string
	err       error
}

func (r attributionOwnerStub) AccountIDForSession(context.Context, string) (string, error) {
	return r.accountID, r.err
}

type attributionApplierStub struct {
	updated   recordsv1.VoiceTurn
	err       error
	accountID string
	calls     []recordsv1.UpdateAttributionRequest
}

func (a *attributionApplierStub) CorrectAttributionIfUnresolved(_ context.Context, accountID, _ string, request recordsv1.UpdateAttributionRequest, _ bool) (recordsv1.VoiceTurn, error) {
	a.accountID = accountID
	a.calls = append(a.calls, request)
	if a.err != nil {
		return recordsv1.VoiceTurn{}, a.err
	}
	return a.updated, nil
}
