package sessions

import (
	"context"
	"errors"
	"testing"
)

func TestServiceStartDifferentKeyPendingReturnsInProgressBeforeReadiness(t *testing.T) {
	assertDifferentKeyReturnsInProgressBeforeReadiness(t, StartOperationPending)
}

func TestServiceStartDifferentKeyCompensatingReturnsInProgressBeforeReadiness(t *testing.T) {
	assertDifferentKeyReturnsInProgressBeforeReadiness(t, StartOperationCompensating)
}

func TestServiceStartDifferentKeyCompensationFailedReturnsInProgressBeforeReadiness(t *testing.T) {
	assertDifferentKeyReturnsInProgressBeforeReadiness(
		t,
		StartOperationCompensationFailed,
	)
}

func assertDifferentKeyReturnsInProgressBeforeReadiness(
	t *testing.T,
	status StartOperationStatus,
) {
	t.Helper()
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.operation = startOperationWithStatus(fixture, status)
	fixture.languages.err = ErrLanguageConfigNotReady
	fixture.connections.err = ErrWebRTCUnavailable
	input := validStartInput()
	input.IdempotencyKey = "start_2"
	input.RequestHash = "hash_2"

	_, err := fixture.service.Start(context.Background(), input)
	if !errors.Is(err, ErrSessionStartInProgress) {
		t.Fatalf("Start() error = %v, want ErrSessionStartInProgress", err)
	}
	if fixture.languages.calls != 0 ||
		fixture.connections.calls != 0 ||
		fixture.realtime.startCalls != 0 ||
		fixture.repository.beginCalls != 0 {
		t.Fatalf(
			"calls = language %d, WebRTC %d, realtime %d, begin %d; want all 0",
			fixture.languages.calls,
			fixture.connections.calls,
			fixture.realtime.startCalls,
			fixture.repository.beginCalls,
		)
	}
}

func TestServiceStartNewKeyAllowedAfterCompensatedOperation(t *testing.T) {
	fixture := newStartFixture(t, StatusCreated)
	fixture.repository.operation = startOperationWithStatus(
		fixture,
		StartOperationCompensated,
	)
	fixture.service.deps.IDs = &fakeIDGenerator{id: "op_2"}
	fixture.realtime.startResult.StartOperationID = "op_2"
	input := validStartInput()
	input.IdempotencyKey = "start_2"
	input.RequestHash = "hash_2"

	session, err := fixture.service.Start(context.Background(), input)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if session.Status != StatusActive ||
		fixture.repository.operation.Status != StartOperationCompleted ||
		fixture.repository.operation.ID != "op_2" {
		t.Fatalf("session = %#v, operation = %#v; want active with completed op_2",
			session, fixture.repository.operation)
	}
	if fixture.repository.beginCalls != 1 || fixture.realtime.startCalls != 1 {
		t.Fatalf("calls = begin %d, realtime %d; want 1, 1",
			fixture.repository.beginCalls, fixture.realtime.startCalls)
	}
}

func startOperationWithStatus(
	fixture *startFixture,
	status StartOperationStatus,
) *StartOperation {
	operation := pendingStartOperationForFixture(fixture)
	operation.Status = status
	if status == StartOperationCompensating {
		claimID := "claim_1"
		operation.CompensationClaimID = &claimID
	}
	return operation
}
