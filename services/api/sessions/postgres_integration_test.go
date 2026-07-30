//go:build integration

package sessions_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testDatabaseURLName = "RECORDSTORE_TEST_DATABASE_URL"

func TestPostgresRepositoryCreateAndQueries(t *testing.T) {
	pool := sessionTestDatabase(t)
	repository := sessions.NewPostgresRepository(pool)
	now := time.Date(2026, time.July, 29, 10, 0, 0, 0, time.UTC)
	insertAccount(t, pool, "acct_1", now.Add(-time.Hour))
	insertAccount(t, pool, "acct_2", now.Add(-time.Hour))

	first, replayed, err := repository.Create(t.Context(), createParams("vs_1", "acct_1", "create_1", "hash_1", now))
	if err != nil || replayed || first.Status != sessions.StatusCreated {
		t.Fatalf("first Create() = %#v, %t, %v", first, replayed, err)
	}
	replayedSession, replayed, err := repository.Create(t.Context(), createParams("vs_unused", "acct_1", "create_1", "hash_1", now))
	if err != nil || !replayed || replayedSession.ID != first.ID {
		t.Fatalf("replayed Create() = %#v, %t, %v", replayedSession, replayed, err)
	}
	if _, _, err := repository.Create(t.Context(), createParams("vs_conflict", "acct_1", "create_1", "other", now)); !errors.Is(err, sessions.ErrIdempotencyKeyConflict) {
		t.Fatalf("conflicting Create() error = %v", err)
	}
	if _, _, err := repository.Create(t.Context(), createParams("vs_2", "acct_2", "create_1", "hash_2", now)); err != nil {
		t.Fatalf("cross-account Create() error = %v", err)
	}

	owned, err := repository.GetOwned(t.Context(), "acct_1", "vs_1")
	if err != nil || owned.ID != "vs_1" {
		t.Fatalf("GetOwned() = %#v, %v", owned, err)
	}
	if _, err := repository.GetOwned(t.Context(), "acct_2", "vs_1"); !errors.Is(err, sessions.ErrVoiceSessionNotFound) {
		t.Fatalf("cross-account GetOwned() error = %v", err)
	}
	snapshot, err := repository.GetSession(t.Context(), "vs_1")
	if err != nil || snapshot.AccountID != "acct_1" || snapshot.Status != sessions.StatusCreated {
		t.Fatalf("GetSession() = %#v, %v", snapshot, err)
	}

	for index := 3; index <= 7; index++ {
		id := fmt.Sprintf("vs_%d", index)
		at := now.Add(time.Duration(index) * time.Minute)
		if _, _, err := repository.Create(t.Context(), createParams(id, "acct_1", "create_"+id, "hash_"+id, at)); err != nil {
			t.Fatalf("Create(%q) error = %v", id, err)
		}
	}
	firstPage, err := repository.List(t.Context(), sessions.ListFilter{AccountID: "acct_1", Limit: 3})
	if err != nil || len(firstPage.Sessions) != 3 || firstPage.NextCursor == nil {
		t.Fatalf("first List() = %#v, %v", firstPage, err)
	}
	secondPage, err := repository.List(t.Context(), sessions.ListFilter{
		AccountID: "acct_1", Limit: 3, Cursor: *firstPage.NextCursor,
	})
	if err != nil || len(secondPage.Sessions) != 3 {
		t.Fatalf("second List() = %#v, %v", secondPage, err)
	}
	seen := make(map[string]bool)
	for _, item := range append(firstPage.Sessions, secondPage.Sessions...) {
		if seen[item.ID] {
			t.Fatalf("duplicate paged session %q", item.ID)
		}
		seen[item.ID] = true
	}
	status := sessions.StatusCreated
	filtered, err := repository.List(t.Context(), sessions.ListFilter{
		AccountID: "acct_1", Status: &status, Limit: 100,
	})
	if err != nil || len(filtered.Sessions) != 6 {
		t.Fatalf("filtered List() = %#v, %v", filtered, err)
	}
	if _, err := repository.List(t.Context(), sessions.ListFilter{
		AccountID: "acct_1", Limit: 10, Cursor: "broken",
	}); !errors.Is(err, sessions.ErrInvalidRequest) {
		t.Fatalf("invalid cursor List() error = %v", err)
	}
	empty, err := repository.List(t.Context(), sessions.ListFilter{AccountID: "acct_empty", Limit: 10})
	if err != nil || empty.Sessions == nil || len(empty.Sessions) != 0 {
		t.Fatalf("empty List() = %#v, %v", empty, err)
	}
}

func TestPostgresRepositoryMergedAccountKeepsOriginalSessionOwner(t *testing.T) {
	pool := sessionTestDatabase(t)
	repository := sessions.NewPostgresRepository(pool)
	now := time.Date(2026, time.July, 30, 9, 0, 0, 0, time.UTC)

	insertAccount(t, pool, "acct_anonymous", now.Add(-time.Hour))
	insertRegisteredAccount(t, pool, "acct_registered", "phone_registered", now.Add(-time.Hour))
	insertAccount(t, pool, "acct_unrelated", now.Add(-time.Hour))
	mergeAccount(t, pool, "acct_anonymous", "acct_registered")

	createSession(t, repository, "vs_anonymous", "acct_anonymous", now)
	createSession(t, repository, "vs_registered", "acct_registered", now.Add(time.Second))
	createSession(t, repository, "vs_unrelated", "acct_unrelated", now.Add(2*time.Second))

	owned, err := repository.GetOwned(
		t.Context(), "acct_registered", "vs_anonymous",
	)
	if err != nil || owned.AccountID != "acct_anonymous" {
		t.Fatalf("merged GetOwned() = %#v, %v", owned, err)
	}
	if _, err := repository.GetOwned(
		t.Context(), "acct_registered", "vs_unrelated",
	); !errors.Is(err, sessions.ErrVoiceSessionNotFound) {
		t.Fatalf("unrelated GetOwned() error = %v", err)
	}
	page, err := repository.List(t.Context(), sessions.ListFilter{
		AccountID: "acct_registered",
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("merged List() error = %v", err)
	}
	listed := make(map[string]string, len(page.Sessions))
	for _, item := range page.Sessions {
		listed[item.ID] = item.AccountID
	}
	if listed["vs_anonymous"] != "acct_anonymous" ||
		listed["vs_registered"] != "acct_registered" {
		t.Fatalf("merged List() owners = %#v", listed)
	}
	if _, exists := listed["vs_unrelated"]; exists {
		t.Fatalf("merged List() exposed unrelated session: %#v", listed)
	}

	begin := beginParams(
		"op_merged", "vs_anonymous", "acct_registered",
		"start_merged", "hash_start_merged", now.Add(3*time.Second),
	)
	started, err := repository.BeginStartOperation(t.Context(), begin)
	if err != nil || started.Operation.AccountID != "acct_anonymous" {
		t.Fatalf("merged BeginStartOperation() = %#v, %v", started, err)
	}
	operation, err := repository.GetStartOperation(
		t.Context(), "acct_registered", "vs_anonymous", "start_merged",
	)
	if err != nil || operation.AccountID != "acct_anonymous" {
		t.Fatalf("merged GetStartOperation() = %#v, %v", operation, err)
	}
	if _, err := repository.GetStartOperation(
		t.Context(), "acct_unrelated", "vs_anonymous", "start_merged",
	); !errors.Is(err, sessions.ErrVoiceSessionNotFound) {
		t.Fatalf("unrelated GetStartOperation() error = %v", err)
	}
	active, replayed, err := repository.TransitionToActive(
		t.Context(),
		sessions.StartTransitionParams{
			SessionID: "vs_anonymous", AccountID: "acct_registered",
			OperationID: "op_merged", Expected: sessions.StatusCreated,
			StartedAt:      now.Add(4 * time.Second),
			IdempotencyKey: "start_merged", RequestHash: "hash_start_merged",
		},
	)
	if err != nil || replayed || active.Status != sessions.StatusActive ||
		active.AccountID != "acct_anonymous" {
		t.Fatalf("merged TransitionToActive() = %#v, %t, %v", active, replayed, err)
	}

	intent := endIntent(
		"vs_anonymous", "acct_registered", "end_merged", "hash_end_merged",
		now.Add(5*time.Second),
	)
	saved, replayed, err := repository.SaveEndIntent(t.Context(), intent)
	if err != nil || replayed || saved.AccountID != "acct_anonymous" {
		t.Fatalf("merged SaveEndIntent() = %#v, %t, %v", saved, replayed, err)
	}
	ended, err := repository.TransitionToEnded(
		t.Context(),
		sessions.EndTransitionParams{
			SessionID: "vs_anonymous", AccountID: "acct_registered",
			Expected: sessions.StatusActive, EndedAt: now.Add(6 * time.Second),
			EndReason: sessions.EndReasonUserRequested,
		},
	)
	if err != nil || ended.Status != sessions.StatusEnded ||
		ended.AccountID != "acct_anonymous" {
		t.Fatalf("merged TransitionToEnded() = %#v, %v", ended, err)
	}
	if err := repository.CompleteEndIntent(
		t.Context(), "acct_registered", "vs_anonymous", now.Add(7*time.Second),
	); err != nil {
		t.Fatalf("merged CompleteEndIntent() error = %v", err)
	}
	storedIntent, err := repository.GetEndIntent(
		t.Context(), "acct_registered", "vs_anonymous",
	)
	if err != nil || storedIntent.AccountID != "acct_anonymous" ||
		!storedIntent.Completed() {
		t.Fatalf("merged GetEndIntent() = %#v, %v", storedIntent, err)
	}

	assertLifecycleOwners(
		t, pool, "vs_anonymous", "acct_anonymous", "op_merged",
	)

	createSession(
		t, repository, "vs_failed", "acct_anonymous", now.Add(8*time.Second),
	)
	if _, err := repository.BeginStartOperation(
		t.Context(),
		beginParams(
			"op_failed", "vs_failed", "acct_registered",
			"start_failed", "hash_start_failed", now.Add(9*time.Second),
		),
	); err != nil {
		t.Fatalf("failed-path BeginStartOperation() error = %v", err)
	}
	if _, _, err := repository.TransitionToActive(
		t.Context(),
		sessions.StartTransitionParams{
			SessionID: "vs_failed", AccountID: "acct_registered",
			OperationID: "op_failed", Expected: sessions.StatusCreated,
			StartedAt:      now.Add(10 * time.Second),
			IdempotencyKey: "start_failed", RequestHash: "hash_start_failed",
		},
	); err != nil {
		t.Fatalf("failed-path TransitionToActive() error = %v", err)
	}
	failed, err := repository.TransitionToFailed(
		t.Context(),
		sessions.FailureTransitionParams{
			SessionID: "vs_failed", AccountID: "acct_registered",
			Expected: sessions.StatusActive, FailedAt: now.Add(11 * time.Second),
			ErrorCode: "runtime_failed",
		},
	)
	if err != nil || failed.Status != sessions.StatusFailed ||
		failed.AccountID != "acct_anonymous" {
		t.Fatalf("merged TransitionToFailed() = %#v, %v", failed, err)
	}
}

func TestPostgresRepositoryMergedAccountCanClaimCompensation(t *testing.T) {
	pool := sessionTestDatabase(t)
	repository := sessions.NewPostgresRepository(pool)
	now := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)

	insertAccount(t, pool, "acct_source", now.Add(-time.Hour))
	insertAccount(t, pool, "acct_intermediate", now.Add(-time.Hour))
	insertRegisteredAccount(t, pool, "acct_actor", "phone_actor", now.Add(-time.Hour))
	insertAccount(t, pool, "acct_other", now.Add(-time.Hour))
	mergeAccount(t, pool, "acct_source", "acct_intermediate")
	mergeAccount(t, pool, "acct_intermediate", "acct_actor")
	createSession(t, repository, "vs_compensation", "acct_source", now)

	if _, err := repository.BeginStartOperation(
		t.Context(),
		beginParams(
			"op_compensation", "vs_compensation", "acct_actor",
			"start_compensation", "hash_compensation", now.Add(time.Second),
		),
	); err != nil {
		t.Fatalf("multilevel BeginStartOperation() error = %v", err)
	}
	claimParams := sessions.ClaimStartCompensationParams{
		SessionID: "vs_compensation", AccountID: "acct_actor",
		OperationID: "op_compensation", ClaimID: "claim_compensation",
		ClaimedAt: now.Add(2 * time.Second),
	}
	claim, err := repository.ClaimStartCompensation(t.Context(), claimParams)
	if err != nil || !claim.Claimed {
		t.Fatalf("multilevel ClaimStartCompensation() = %#v, %v", claim, err)
	}
	otherClaim := claimParams
	otherClaim.AccountID = "acct_other"
	otherClaim.ClaimID = "claim_other"
	if _, err := repository.ClaimStartCompensation(
		t.Context(), otherClaim,
	); !errors.Is(err, sessions.ErrVoiceSessionNotFound) {
		t.Fatalf("unrelated ClaimStartCompensation() error = %v", err)
	}
	if err := repository.FailStartCompensation(
		t.Context(),
		sessions.FailStartCompensationParams{
			SessionID: "vs_compensation", AccountID: "acct_actor",
			OperationID: "op_compensation", ClaimID: "claim_compensation",
			FailedAt: now.Add(3 * time.Second),
		},
	); err != nil {
		t.Fatalf("multilevel FailStartCompensation() error = %v", err)
	}
	if err := repository.CompleteStartCompensation(
		t.Context(),
		sessions.CompleteStartCompensationParams{
			SessionID: "vs_compensation", AccountID: "acct_actor",
			OperationID: "op_compensation", ClaimID: "claim_compensation",
			CompletedAt: now.Add(4 * time.Second),
		},
	); err != nil {
		t.Fatalf("multilevel CompleteStartCompensation() error = %v", err)
	}

	var owner string
	if err := pool.QueryRow(t.Context(), `
		SELECT account_id
		FROM voice_session_start_operations
		WHERE operation_id = 'op_compensation'
	`).Scan(&owner); err != nil {
		t.Fatalf("read compensation owner: %v", err)
	}
	if owner != "acct_source" {
		t.Fatalf("compensation operation owner = %q, want acct_source", owner)
	}
}

func TestPostgresRepositoryStartCompensationAndTransitions(t *testing.T) {
	pool := sessionTestDatabase(t)
	repository := sessions.NewPostgresRepository(pool)
	now := time.Date(2026, time.July, 29, 11, 0, 0, 0, time.UTC)
	insertAccount(t, pool, "acct_start", now.Add(-time.Hour))
	createSession(t, repository, "vs_start", "acct_start", now)

	firstBegin := beginParams("op_1", "vs_start", "acct_start", "start_1", "hash_1", now.Add(time.Second))
	begin, err := repository.BeginStartOperation(t.Context(), firstBegin)
	if err != nil || begin.Replayed || begin.Operation.Status != sessions.StartOperationPending {
		t.Fatalf("BeginStartOperation() = %#v, %v", begin, err)
	}
	replayParams := firstBegin
	replayParams.OperationID = "op_unused"
	replay, err := repository.BeginStartOperation(t.Context(), replayParams)
	if err != nil || !replay.Replayed || replay.Operation.ID != "op_1" {
		t.Fatalf("replayed BeginStartOperation() = %#v, %v", replay, err)
	}
	hashConflict := replayParams
	hashConflict.RequestHash = "other"
	if _, err := repository.BeginStartOperation(t.Context(), hashConflict); !errors.Is(err, sessions.ErrIdempotencyKeyConflict) {
		t.Fatalf("conflicting BeginStartOperation() error = %v", err)
	}
	if _, err := repository.GetStartOperation(t.Context(), "acct_start", "vs_start", "other"); !errors.Is(err, sessions.ErrSessionStartInProgress) {
		t.Fatalf("occupied GetStartOperation() error = %v", err)
	}

	mismatch, err := repository.ClaimStartCompensation(t.Context(), sessions.ClaimStartCompensationParams{
		SessionID: "vs_start", AccountID: "acct_start", OperationID: "other",
		ClaimID: "claim_0", ClaimedAt: now.Add(2 * time.Second),
	})
	if err != nil || mismatch.Claimed || mismatch.Reason != sessions.StartCompensationOperationMismatch {
		t.Fatalf("mismatched ClaimStartCompensation() = %#v, %v", mismatch, err)
	}
	claimParams := sessions.ClaimStartCompensationParams{
		SessionID: "vs_start", AccountID: "acct_start", OperationID: "op_1",
		ClaimID: "claim_1", ClaimedAt: now.Add(2 * time.Second),
	}
	claim, err := repository.ClaimStartCompensation(t.Context(), claimParams)
	if err != nil || !claim.Claimed {
		t.Fatalf("ClaimStartCompensation() = %#v, %v", claim, err)
	}
	otherClaim := claimParams
	otherClaim.ClaimID = "claim_2"
	denied, err := repository.ClaimStartCompensation(t.Context(), otherClaim)
	if err != nil || denied.Claimed {
		t.Fatalf("competing ClaimStartCompensation() = %#v, %v", denied, err)
	}
	reentry, err := repository.ClaimStartCompensation(t.Context(), claimParams)
	if err != nil || !reentry.Claimed {
		t.Fatalf("reentered ClaimStartCompensation() = %#v, %v", reentry, err)
	}
	fail := sessions.FailStartCompensationParams{
		SessionID: "vs_start", AccountID: "acct_start", OperationID: "op_1",
		ClaimID: "claim_1", FailedAt: now.Add(3 * time.Second),
	}
	if err := repository.FailStartCompensation(t.Context(), fail); err != nil {
		t.Fatalf("FailStartCompensation() error = %v", err)
	}
	if err := repository.FailStartCompensation(t.Context(), fail); err != nil {
		t.Fatalf("replayed FailStartCompensation() error = %v", err)
	}
	complete := sessions.CompleteStartCompensationParams{
		SessionID: "vs_start", AccountID: "acct_start", OperationID: "op_1",
		ClaimID: "claim_1", CompletedAt: now.Add(4 * time.Second),
	}
	if err := repository.CompleteStartCompensation(t.Context(), complete); err != nil {
		t.Fatalf("CompleteStartCompensation() after failure error = %v", err)
	}
	if err := repository.CompleteStartCompensation(t.Context(), complete); err != nil {
		t.Fatalf("replayed CompleteStartCompensation() error = %v", err)
	}
	if _, err := repository.GetStartOperation(t.Context(), "acct_start", "vs_start", "start_1"); !errors.Is(err, sessions.ErrStartOperationNotFound) {
		t.Fatalf("compensated GetStartOperation() error = %v", err)
	}

	second := beginParams("op_2", "vs_start", "acct_start", "start_2", "hash_2", now.Add(5*time.Second))
	if _, err := repository.BeginStartOperation(t.Context(), second); err != nil {
		t.Fatalf("second BeginStartOperation() error = %v", err)
	}
	startedAt := now.Add(6 * time.Second)
	active, replayed, err := repository.TransitionToActive(t.Context(), sessions.StartTransitionParams{
		SessionID: "vs_start", AccountID: "acct_start", OperationID: "op_2",
		Expected: sessions.StatusCreated, StartedAt: startedAt,
		IdempotencyKey: "start_2", RequestHash: "hash_2",
	})
	if err != nil || replayed || active.Status != sessions.StatusActive {
		t.Fatalf("TransitionToActive() = %#v, %t, %v", active, replayed, err)
	}
	activeReplay, replayed, err := repository.TransitionToActive(t.Context(), sessions.StartTransitionParams{
		SessionID: "vs_start", AccountID: "acct_start", OperationID: "op_2",
		Expected: sessions.StatusCreated, StartedAt: startedAt.Add(time.Minute),
		IdempotencyKey: "start_2", RequestHash: "hash_2",
	})
	if err != nil || !replayed || !activeReplay.StartedAt.Equal(startedAt) {
		t.Fatalf("replayed TransitionToActive() = %#v, %t, %v", activeReplay, replayed, err)
	}
	if _, err := repository.GetStartOperation(t.Context(), "acct_start", "vs_start", "unused_key"); !errors.Is(err, sessions.ErrStartOperationNotFound) {
		t.Fatalf("completed GetStartOperation() with new key error = %v", err)
	}
	deniedAfterActivation, err := repository.ClaimStartCompensation(t.Context(), sessions.ClaimStartCompensationParams{
		SessionID: "vs_start", AccountID: "acct_start", OperationID: "op_2",
		ClaimID: "claim_after_active", ClaimedAt: startedAt.Add(time.Second),
	})
	if err != nil || deniedAfterActivation.Claimed ||
		deniedAfterActivation.Reason != sessions.StartCompensationSessionNotCreated {
		t.Fatalf("post-activation ClaimStartCompensation() = %#v, %v", deniedAfterActivation, err)
	}
	failed, err := repository.TransitionToFailed(t.Context(), sessions.FailureTransitionParams{
		SessionID: "vs_start", AccountID: "acct_start", Expected: sessions.StatusActive,
		FailedAt: startedAt.Add(time.Second), ErrorCode: "runtime_failed",
	})
	if err != nil || failed.Status != sessions.StatusFailed ||
		failed.EndedAt == nil || !failed.EndedAt.Equal(startedAt.Add(time.Second)) {
		t.Fatalf("TransitionToFailed() = %#v, %v", failed, err)
	}
}

func TestPostgresRepositoryEndIntentAndInterlocks(t *testing.T) {
	pool := sessionTestDatabase(t)
	repository := sessions.NewPostgresRepository(pool)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	insertAccount(t, pool, "acct_end", now.Add(-time.Hour))
	createSession(t, repository, "vs_end", "acct_end", now)

	if _, err := repository.BeginStartOperation(t.Context(), beginParams(
		"op_end", "vs_end", "acct_end", "start_end", "hash_start", now.Add(time.Second),
	)); err != nil {
		t.Fatalf("BeginStartOperation() error = %v", err)
	}
	intent := endIntent("vs_end", "acct_end", "end_1", "hash_end", now.Add(2*time.Second))
	if _, _, err := repository.SaveEndIntent(t.Context(), intent); !errors.Is(err, sessions.ErrSessionStartInProgress) {
		t.Fatalf("SaveEndIntent() with pending start error = %v", err)
	}
	claim := sessions.ClaimStartCompensationParams{
		SessionID: "vs_end", AccountID: "acct_end", OperationID: "op_end",
		ClaimID: "claim_end", ClaimedAt: now.Add(3 * time.Second),
	}
	if result, err := repository.ClaimStartCompensation(t.Context(), claim); err != nil || !result.Claimed {
		t.Fatalf("ClaimStartCompensation() = %#v, %v", result, err)
	}
	if err := repository.CompleteStartCompensation(t.Context(), sessions.CompleteStartCompensationParams{
		SessionID: "vs_end", AccountID: "acct_end", OperationID: "op_end",
		ClaimID: "claim_end", CompletedAt: now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("CompleteStartCompensation() error = %v", err)
	}
	saved, replayed, err := repository.SaveEndIntent(t.Context(), intent)
	if err != nil || replayed || saved.Completed() {
		t.Fatalf("SaveEndIntent() = %#v, %t, %v", saved, replayed, err)
	}
	replayedIntent, replayed, err := repository.SaveEndIntent(t.Context(), intent)
	if err != nil || !replayed || replayedIntent.IdempotencyKey != intent.IdempotencyKey {
		t.Fatalf("replayed SaveEndIntent() = %#v, %t, %v", replayedIntent, replayed, err)
	}
	conflict := intent
	conflict.RequestHash = "other"
	if _, _, err := repository.SaveEndIntent(t.Context(), conflict); !errors.Is(err, sessions.ErrIdempotencyKeyConflict) {
		t.Fatalf("conflicting SaveEndIntent() error = %v", err)
	}
	if _, err := repository.BeginStartOperation(t.Context(), beginParams(
		"op_blocked", "vs_end", "acct_end", "start_blocked", "hash", now.Add(5*time.Second),
	)); !errors.Is(err, sessions.ErrConcurrentTransition) {
		t.Fatalf("BeginStartOperation() with end intent error = %v", err)
	}
	ended, err := repository.TransitionToEnded(t.Context(), sessions.EndTransitionParams{
		SessionID: "vs_end", AccountID: "acct_end", Expected: sessions.StatusCreated,
		EndedAt: now.Add(6 * time.Second), EndReason: sessions.EndReasonUserRequested,
	})
	if err != nil || ended.Status != sessions.StatusEnded || ended.StartedAt != nil {
		t.Fatalf("created TransitionToEnded() = %#v, %v", ended, err)
	}
	completedAt := now.Add(7 * time.Second)
	if err := repository.CompleteEndIntent(t.Context(), "acct_end", "vs_end", completedAt); err != nil {
		t.Fatalf("CompleteEndIntent() error = %v", err)
	}
	if err := repository.CompleteEndIntent(t.Context(), "acct_end", "vs_end", completedAt); err != nil {
		t.Fatalf("replayed CompleteEndIntent() error = %v", err)
	}
	stored, err := repository.GetEndIntent(t.Context(), "acct_end", "vs_end")
	if err != nil || stored.CompletedAt == nil || !stored.CompletedAt.Equal(completedAt) {
		t.Fatalf("GetEndIntent() = %#v, %v", stored, err)
	}
	if _, err := repository.GetEndIntent(t.Context(), "other", "vs_end"); !errors.Is(err, sessions.ErrEndIntentNotFound) {
		t.Fatalf("cross-account GetEndIntent() error = %v", err)
	}
}

func TestPostgresRepositoryActiveToEnded(t *testing.T) {
	pool := sessionTestDatabase(t)
	repository := sessions.NewPostgresRepository(pool)
	now := time.Date(2026, time.July, 29, 12, 30, 0, 0, time.UTC)
	insertAccount(t, pool, "acct_active_end", now.Add(-time.Hour))
	createSession(t, repository, "vs_active_end", "acct_active_end", now)
	if _, err := repository.BeginStartOperation(t.Context(), beginParams(
		"op_active_end", "vs_active_end", "acct_active_end",
		"start_active_end", "hash_start_active_end", now.Add(time.Second),
	)); err != nil {
		t.Fatalf("BeginStartOperation() error = %v", err)
	}
	startedAt := now.Add(2 * time.Second)
	if _, _, err := repository.TransitionToActive(t.Context(), sessions.StartTransitionParams{
		SessionID: "vs_active_end", AccountID: "acct_active_end",
		OperationID: "op_active_end", Expected: sessions.StatusCreated,
		StartedAt: startedAt, IdempotencyKey: "start_active_end",
		RequestHash: "hash_start_active_end",
	}); err != nil {
		t.Fatalf("TransitionToActive() error = %v", err)
	}
	if _, _, err := repository.SaveEndIntent(t.Context(), endIntent(
		"vs_active_end", "acct_active_end", "end_active", "hash_end_active",
		now.Add(3*time.Second),
	)); err != nil {
		t.Fatalf("SaveEndIntent() error = %v", err)
	}
	endedAt := now.Add(4 * time.Second)
	ended, err := repository.TransitionToEnded(t.Context(), sessions.EndTransitionParams{
		SessionID: "vs_active_end", AccountID: "acct_active_end",
		Expected: sessions.StatusActive, EndedAt: endedAt,
		EndReason: sessions.EndReasonUserRequested,
	})
	if err != nil || ended.Status != sessions.StatusEnded ||
		ended.StartedAt == nil || !ended.StartedAt.Equal(startedAt) ||
		ended.EndedAt == nil || !ended.EndedAt.Equal(endedAt) {
		t.Fatalf("active TransitionToEnded() = %#v, %v", ended, err)
	}
}

func TestPostgresRepositoryConcurrentAuthority(t *testing.T) {
	pool := sessionTestDatabase(t)
	repository := sessions.NewPostgresRepository(pool)
	now := time.Date(2026, time.July, 29, 13, 0, 0, 0, time.UTC)
	insertAccount(t, pool, "acct_concurrent", now.Add(-time.Hour))

	const writers = 16
	start := make(chan struct{})
	results := make(chan error, writers)
	var group sync.WaitGroup
	for writer := range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, _, err := repository.Create(t.Context(), createParams(
				fmt.Sprintf("vs_race_%02d", writer), "acct_concurrent",
				"create_race", "hash_race", now,
			))
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Create() error = %v", err)
		}
	}
	var sessionsCount, requestsCount int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM voice_sessions WHERE account_id = 'acct_concurrent'`).Scan(&sessionsCount); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM voice_session_create_requests WHERE account_id = 'acct_concurrent'`).Scan(&requestsCount); err != nil {
		t.Fatalf("count create requests: %v", err)
	}
	if sessionsCount != 1 || requestsCount != 1 {
		t.Fatalf("race persisted %d sessions and %d requests", sessionsCount, requestsCount)
	}

	insertAccount(t, pool, "acct_hash_race", now.Add(-time.Hour))
	hashRaceStart := make(chan struct{})
	hashRaceErrors := make(chan error, 2)
	for index := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-hashRaceStart
			_, _, err := repository.Create(t.Context(), createParams(
				fmt.Sprintf("vs_hash_race_%d", index), "acct_hash_race",
				"create_hash_race", fmt.Sprintf("hash_%d", index), now,
			))
			hashRaceErrors <- err
		}()
	}
	close(hashRaceStart)
	group.Wait()
	close(hashRaceErrors)
	hashSuccesses, hashConflicts := 0, 0
	for err := range hashRaceErrors {
		switch {
		case err == nil:
			hashSuccesses++
		case errors.Is(err, sessions.ErrIdempotencyKeyConflict):
			hashConflicts++
		default:
			t.Fatalf("concurrent conflicting Create() error = %v", err)
		}
	}
	if hashSuccesses != 1 || hashConflicts != 1 {
		t.Fatalf("conflicting Create race = %d successes, %d conflicts", hashSuccesses, hashConflicts)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM voice_sessions WHERE account_id = 'acct_hash_race'
	`).Scan(&sessionsCount); err != nil {
		t.Fatalf("count hash-race sessions: %v", err)
	}
	if sessionsCount != 1 {
		t.Fatalf("conflicting Create race persisted %d sessions", sessionsCount)
	}

	var sessionID string
	if err := pool.QueryRow(t.Context(), `SELECT id FROM voice_sessions WHERE account_id = 'acct_concurrent'`).Scan(&sessionID); err != nil {
		t.Fatalf("read winning session: %v", err)
	}
	beginStart := make(chan struct{})
	beginErrors := make(chan error, 2)
	for index := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-beginStart
			_, err := repository.BeginStartOperation(t.Context(), beginParams(
				fmt.Sprintf("op_race_%d", index), sessionID, "acct_concurrent",
				fmt.Sprintf("start_race_%d", index), fmt.Sprintf("hash_%d", index),
				now.Add(time.Second),
			))
			beginErrors <- err
		}()
	}
	close(beginStart)
	group.Wait()
	close(beginErrors)
	successes, conflicts := 0, 0
	for err := range beginErrors {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, sessions.ErrSessionStartInProgress):
			conflicts++
		default:
			t.Fatalf("concurrent BeginStartOperation() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent Begin results = %d successes, %d conflicts", successes, conflicts)
	}

	var operationID string
	if err := pool.QueryRow(t.Context(), `
		SELECT operation_id FROM voice_session_start_operations
		WHERE session_id = $1 AND status = 'pending'`, sessionID).Scan(&operationID); err != nil {
		t.Fatalf("read pending operation: %v", err)
	}
	claimStart := make(chan struct{})
	claimResults := make(chan sessions.ClaimStartCompensationResult, 2)
	for index := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-claimStart
			result, err := repository.ClaimStartCompensation(t.Context(), sessions.ClaimStartCompensationParams{
				SessionID: sessionID, AccountID: "acct_concurrent", OperationID: operationID,
				ClaimID: fmt.Sprintf("claim_%d", index), ClaimedAt: now.Add(2 * time.Second),
			})
			if err != nil {
				t.Errorf("concurrent ClaimStartCompensation() error = %v", err)
			}
			claimResults <- result
		}()
	}
	close(claimStart)
	group.Wait()
	close(claimResults)
	claimed := 0
	for result := range claimResults {
		if result.Claimed {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("concurrent claims = %d owners, want 1", claimed)
	}
}

func TestPostgresRepositoryBeginAndEndInterlockConcurrently(t *testing.T) {
	pool := sessionTestDatabase(t)
	repository := sessions.NewPostgresRepository(pool)
	now := time.Date(2026, time.July, 29, 14, 0, 0, 0, time.UTC)
	insertAccount(t, pool, "acct_interlock", now.Add(-time.Hour))
	createSession(t, repository, "vs_interlock", "acct_interlock", now)

	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		_, err := repository.BeginStartOperation(t.Context(), beginParams(
			"op_interlock", "vs_interlock", "acct_interlock",
			"start_interlock", "hash_start", now.Add(time.Second),
		))
		results <- err
	}()
	go func() {
		defer group.Done()
		<-start
		_, _, err := repository.SaveEndIntent(t.Context(), endIntent(
			"vs_interlock", "acct_interlock", "end_interlock", "hash_end",
			now.Add(time.Second),
		))
		results <- err
	}()
	close(start)
	group.Wait()
	close(results)

	successes, blocked := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, sessions.ErrSessionStartInProgress),
			errors.Is(err, sessions.ErrConcurrentTransition):
			blocked++
		default:
			t.Fatalf("concurrent Begin/End error = %v", err)
		}
	}
	if successes != 1 || blocked != 1 {
		t.Fatalf("concurrent Begin/End = %d successes, %d blocked", successes, blocked)
	}

	var startCount, endCount int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM voice_session_start_operations
		WHERE session_id = 'vs_interlock' AND status = 'pending'`).Scan(&startCount); err != nil {
		t.Fatalf("count interlocked start operations: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*) FROM voice_session_end_intents
		WHERE session_id = 'vs_interlock' AND completed_at IS NULL`).Scan(&endCount); err != nil {
		t.Fatalf("count interlocked end intents: %v", err)
	}
	if startCount+endCount != 1 {
		t.Fatalf("interlock persisted %d starts and %d ends", startCount, endCount)
	}
}

func createParams(id, accountID, key, hash string, at time.Time) sessions.CreateParams {
	return sessions.CreateParams{
		ID: id, AccountID: accountID, IdempotencyKey: key, RequestHash: hash,
		AudioConfig: sessions.DefaultAudioConfig(),
		Capabilities: sessions.Capabilities{
			WebRTC: true, DataChannel: true, Microphone: true,
			Speaker: true, SpeakerDiarization: true,
		},
		CreatedAt: at,
	}
}

func beginParams(id, sessionID, accountID, key, hash string, at time.Time) sessions.BeginStartOperationParams {
	return sessions.BeginStartOperationParams{
		OperationID: id, SessionID: sessionID, AccountID: accountID,
		IdempotencyKey: key, RequestHash: hash, CreatedAt: at,
	}
}

func endIntent(sessionID, accountID, key, hash string, at time.Time) sessions.EndIntent {
	return sessions.EndIntent{
		SessionID: sessionID, AccountID: accountID,
		Reason:         sessions.EndReasonUserRequested,
		IdempotencyKey: key, RequestHash: hash, RequestedAt: at,
	}
}

func createSession(
	t *testing.T,
	repository *sessions.PostgresRepository,
	sessionID string,
	accountID string,
	at time.Time,
) {
	t.Helper()
	if _, _, err := repository.Create(t.Context(), createParams(
		sessionID, accountID, "create_"+sessionID, "hash_"+sessionID, at,
	)); err != nil {
		t.Fatalf("Create(%q) error = %v", sessionID, err)
	}
}

func insertAccount(t *testing.T, pool *pgxpool.Pool, accountID string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_accounts (id, kind, created_at)
		VALUES ($1, 'anonymous', $2)`, accountID, at); err != nil {
		t.Fatalf("insert account %q: %v", accountID, err)
	}
}

func insertRegisteredAccount(
	t *testing.T,
	pool *pgxpool.Pool,
	accountID string,
	phoneHash string,
	at time.Time,
) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_accounts (id, kind, phone_hash, created_at)
		VALUES ($1, 'registered', $2, $3)`,
		accountID, phoneHash, at); err != nil {
		t.Fatalf("insert registered account %q: %v", accountID, err)
	}
}

func mergeAccount(
	t *testing.T,
	pool *pgxpool.Pool,
	sourceAccountID string,
	targetAccountID string,
) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `
		UPDATE lingow_accounts
		SET merged_into = $1
		WHERE id = $2`,
		targetAccountID, sourceAccountID); err != nil {
		t.Fatalf(
			"merge account %q into %q: %v",
			sourceAccountID, targetAccountID, err,
		)
	}
}

func assertLifecycleOwners(
	t *testing.T,
	pool *pgxpool.Pool,
	sessionID string,
	wantOwner string,
	operationID string,
) {
	t.Helper()
	var sessionOwner, operationOwner, intentOwner string
	if err := pool.QueryRow(t.Context(), `
		SELECT account_id FROM voice_sessions WHERE id = $1`,
		sessionID,
	).Scan(&sessionOwner); err != nil {
		t.Fatalf("read session owner: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT account_id
		FROM voice_session_start_operations
		WHERE operation_id = $1`,
		operationID,
	).Scan(&operationOwner); err != nil {
		t.Fatalf("read start operation owner: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `
		SELECT account_id
		FROM voice_session_end_intents
		WHERE session_id = $1`,
		sessionID,
	).Scan(&intentOwner); err != nil {
		t.Fatalf("read end intent owner: %v", err)
	}
	if sessionOwner != wantOwner ||
		operationOwner != wantOwner ||
		intentOwner != wantOwner {
		t.Fatalf(
			"persisted owners = session %q, operation %q, intent %q; want %q",
			sessionOwner, operationOwner, intentOwner, wantOwner,
		)
	}
}

func sessionTestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv(testDatabaseURLName)
	if databaseURL == "" {
		t.Skipf("set %s to run PostgreSQL integration tests", testDatabaseURLName)
	}
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse %s: %v", testDatabaseURLName, err)
	}
	if !strings.HasSuffix(strings.ToLower(adminConfig.ConnConfig.Database), "_test") {
		t.Fatalf("%s must target a database ending in _test", testDatabaseURLName)
	}
	adminConfig.ConnConfig.RuntimeParams["TimeZone"] = "UTC"
	admin, err := pgxpool.NewWithConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	t.Cleanup(admin.Close)

	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("create random schema name: %v", err)
	}
	schema := fmt.Sprintf("sessions_%x", randomBytes)
	if _, err := admin.Exec(t.Context(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
	})

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse isolated database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["TimeZone"] = "UTC"
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET search_path TO "`+schema+`"`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatalf("open isolated integration pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := recordstore.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return pool
}
