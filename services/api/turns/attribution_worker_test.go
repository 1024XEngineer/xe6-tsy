package turns

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestAttributionWorkerAppliesDecisionAndAcks(t *testing.T) {
	applier := &attributionApplierStub{updated: recordsv1.VoiceTurn{ID: "vt_01"}}
	worker, ctx := newAttributionWorkerStub(t,
		&attributionSourceStub{deliveries: []AttributionTaskDelivery{&attributionDeliveryStub{task: taskFixture()}}},
		&fixedDecisionResolver{decision: &AttributionDecision{
			ParticipantID:       "p_02",
			AttributionStatus:   recordsv1.AttributionCorrected,
			SpeakerConfidence:   ptrFloat64(0.91),
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

func newAttributionWorkerStub(t *testing.T, source AttributionTaskSource, resolver AttributionResolver, applier AttributionApplier) (*AttributionWorker, context.Context) {
	t.Helper()
	reader := &attributionReaderStub{
		turn:  recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"},
		parts: []recordsv1.Participant{{ID: "p_01"}},
	}
	worker, err := NewAttributionWorker(source, resolver, reader, applier, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	task    AttributionTask
	acked   bool
	retried bool
	failed  bool
	ackErr  error
}

func (d *attributionDeliveryStub) Task() AttributionTask { return d.task }
func (d *attributionDeliveryStub) Ack() error {
	if d.ackErr != nil {
		return d.ackErr
	}
	d.acked = true
	return nil
}
func (d *attributionDeliveryStub) Retry(string) error {
	d.retried = true
	return nil
}
func (d *attributionDeliveryStub) Fail(string) error {
	d.failed = true
	return nil
}

type fixedDecisionResolver struct {
	decision *AttributionDecision
	err      error
}

func (r *fixedDecisionResolver) Resolve(context.Context, AttributionResolutionInput) (*AttributionDecision, error) {
	return r.decision, r.err
}

type attributionReaderStub struct {
	turn  recordsv1.VoiceTurn
	parts []recordsv1.Participant
	err   error
}

func (r *attributionReaderStub) GetTurn(context.Context, string, string) (recordsv1.VoiceTurn, error) {
	return r.turn, r.err
}

func (r *attributionReaderStub) ListParticipants(context.Context, string, string) ([]recordsv1.Participant, error) {
	return r.parts, r.err
}

type attributionApplierStub struct {
	updated recordsv1.VoiceTurn
	err     error
	calls   []recordsv1.UpdateAttributionRequest
}

func (a *attributionApplierStub) CorrectAttribution(_ context.Context, _, _ string, request recordsv1.UpdateAttributionRequest, _ bool) (recordsv1.VoiceTurn, error) {
	a.calls = append(a.calls, request)
	if a.err != nil {
		return recordsv1.VoiceTurn{}, a.err
	}
	return a.updated, nil
}
