package localruntime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fallbackReplayKey struct {
	sessionID   string
	operationID string
}

type fallbackReplayRecord struct {
	payloadHash       string
	status            string
	processingStarted *time.Time
	processingToken   *string
}

type fallbackReplayPoolStub struct {
	beginErr error
	queryErr error
	now      time.Time
	records  map[fallbackReplayKey]fallbackReplayRecord
}

func (p *fallbackReplayPoolStub) Begin(context.Context) (pgx.Tx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	return fallbackReplayTxStub{pool: p}, nil
}

func (p *fallbackReplayPoolStub) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return p.exec(sql, args...)
}

func (p *fallbackReplayPoolStub) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if p.queryErr != nil {
		return fallbackReplayRowStub{err: p.queryErr}
	}
	key := fallbackReplayKey{sessionID: args[0].(string), operationID: args[1].(string)}
	record, ok := p.records[key]
	if !ok {
		return fallbackReplayRowStub{err: pgx.ErrNoRows}
	}
	return fallbackReplayRowStub{record: record, now: p.now}
}

func (p *fallbackReplayPoolStub) exec(sql string, args ...any) (pgconn.CommandTag, error) {
	key := fallbackReplayKey{sessionID: args[0].(string), operationID: args[1].(string)}
	record, exists := p.records[key]
	if strings.Contains(sql, "INSERT INTO") {
		if exists {
			return pgconn.NewCommandTag("INSERT 0 0"), nil
		}
		token := args[3].(string)
		started := p.now
		p.records[key] = fallbackReplayRecord{payloadHash: args[2].(string), status: "processing", processingStarted: &started, processingToken: &token}
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	if !exists {
		return pgconn.NewCommandTag("UPDATE 0"), nil
	}
	if strings.Contains(sql, "SET status='reclaimable'") {
		record.status = "reclaimable"
		record.processingStarted = nil
		record.processingToken = nil
		p.records[key] = record
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	if strings.Contains(sql, "SET status='processing'") {
		token := args[2].(string)
		started := p.now
		record.status = "processing"
		record.processingStarted = &started
		record.processingToken = &token
		p.records[key] = record
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return pgconn.NewCommandTag("UPDATE 0"), nil
}

type fallbackReplayTxStub struct {
	pgx.Tx
	pool *fallbackReplayPoolStub
}

func (tx fallbackReplayTxStub) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return tx.pool.exec(sql, args...)
}

func (tx fallbackReplayTxStub) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return tx.pool.QueryRow(ctx, sql, args...)
}

func (fallbackReplayTxStub) Commit(context.Context) error   { return nil }
func (fallbackReplayTxStub) Rollback(context.Context) error { return nil }

type fallbackReplayRowStub struct {
	record fallbackReplayRecord
	now    time.Time
	err    error
}

func (r fallbackReplayRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = r.record.payloadHash
	*dest[1].(*string) = r.record.status
	if len(dest) == 5 {
		*dest[2].(**time.Time) = r.record.processingStarted
		*dest[3].(**string) = r.record.processingToken
		*dest[4].(*time.Time) = r.now
		return nil
	}
	*dest[2].(**string) = r.record.processingToken
	return nil
}

func TestPostgresFallbackReplayStoreRejectsInvalidClaims(t *testing.T) {
	store := PostgresFallbackPlaybackReplayStore{}
	if _, err := store.Claim(t.Context(), "", "operation-1", "hash"); err == nil {
		t.Fatal("Claim() succeeded with empty session ID")
	}
	if _, err := store.Claim(t.Context(), "session-1", "", "hash"); err == nil {
		t.Fatal("Claim() succeeded with empty operation ID")
	}
	if _, err := store.Claim(t.Context(), "session-1", "operation-1", ""); err == nil {
		t.Fatal("Claim() succeeded with empty payload hash")
	}
	if err := store.Complete(t.Context(), "", "operation-1", "hash", "token"); err == nil {
		t.Fatal("Complete() succeeded with empty session ID")
	}
	if err := store.Complete(t.Context(), "session-1", "", "hash", "token"); err == nil {
		t.Fatal("Complete() succeeded with empty operation ID")
	}
	if err := store.Complete(t.Context(), "session-1", "operation-1", "", "token"); err == nil {
		t.Fatal("Complete() succeeded with empty payload hash")
	}
	if err := store.Complete(t.Context(), "session-1", "operation-1", "hash", ""); err == nil {
		t.Fatal("Complete() succeeded with empty claim token")
	}
	if err := store.Renew(t.Context(), "", "operation-1", "hash", "token"); err == nil {
		t.Fatal("Renew() succeeded with empty session ID")
	}
	if err := store.Renew(t.Context(), "session-1", "", "hash", "token"); err == nil {
		t.Fatal("Renew() succeeded with empty operation ID")
	}
	if err := store.Renew(t.Context(), "session-1", "operation-1", "", "token"); err == nil {
		t.Fatal("Renew() succeeded with empty payload hash")
	}
	if err := store.Renew(t.Context(), "session-1", "operation-1", "hash", ""); err == nil {
		t.Fatal("Renew() succeeded with empty claim token")
	}
	if err := store.Abort(t.Context(), "", "operation-1", "hash", "token"); err == nil {
		t.Fatal("Abort() succeeded with empty session ID")
	}
	if err := store.Abort(t.Context(), "session-1", "", "hash", "token"); err == nil {
		t.Fatal("Abort() succeeded with empty operation ID")
	}
	if err := store.Abort(t.Context(), "session-1", "operation-1", "", "token"); err == nil {
		t.Fatal("Abort() succeeded with empty payload hash")
	}
	if err := store.Abort(t.Context(), "session-1", "operation-1", "hash", ""); err == nil {
		t.Fatal("Abort() succeeded with empty claim token")
	}
}

func TestPostgresFallbackReplayStoreClaimsAndReplays(t *testing.T) {
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	pastLease := now.Add(-fallbackPlaybackClaimLease - time.Second)
	acceptedToken := "accepted-token"
	processingToken := "processing-token"
	tests := []struct {
		name       string
		payload    string
		record     *fallbackReplayRecord
		wantStatus controlplane.FallbackPlaybackClaimStatus
		wantErr    error
	}{
		{name: "new claim", payload: "hash", wantStatus: controlplane.FallbackPlaybackClaimed},
		{name: "accepted replay", payload: "hash", record: &fallbackReplayRecord{payloadHash: "hash", status: "accepted", processingToken: &acceptedToken}, wantStatus: controlplane.FallbackPlaybackAccepted},
		{name: "active processing replay", payload: "hash", record: &fallbackReplayRecord{payloadHash: "hash", status: "processing", processingStarted: &now, processingToken: &processingToken}, wantStatus: controlplane.FallbackPlaybackProcessing},
		{name: "expired processing is reclaimed", payload: "hash", record: &fallbackReplayRecord{payloadHash: "hash", status: "processing", processingStarted: &pastLease, processingToken: &processingToken}, wantStatus: controlplane.FallbackPlaybackClaimed},
		{name: "payload conflict", payload: "other-hash", record: &fallbackReplayRecord{payloadHash: "hash", status: "accepted"}, wantErr: webrtc.ErrIdempotencyPayloadConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := &fallbackReplayPoolStub{now: now, records: make(map[fallbackReplayKey]fallbackReplayRecord)}
			if tt.record != nil {
				pool.records[fallbackReplayKey{sessionID: "session-1", operationID: "operation-1"}] = *tt.record
			}
			claim, err := (PostgresFallbackPlaybackReplayStore{Pool: pool}).Claim(t.Context(), "session-1", "operation-1", tt.payload)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Claim() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if claim.Status != tt.wantStatus {
				t.Fatalf("Claim() status = %q, want %q", claim.Status, tt.wantStatus)
			}
			if claim.Status == controlplane.FallbackPlaybackClaimed && claim.Token == "" {
				t.Fatal("claimed replay has no claim token")
			}
		})
	}
}

func TestPostgresFallbackReplayStoreHandlesQueryFailuresAndMissingReplays(t *testing.T) {
	t.Run("claim query error", func(t *testing.T) {
		pool := &fallbackReplayPoolStub{
			now:      time.Now(),
			queryErr: errors.New("database unavailable"),
			records: map[fallbackReplayKey]fallbackReplayRecord{
				{sessionID: "session-1", operationID: "operation-1"}: {payloadHash: "hash", status: "accepted"},
			},
		}
		_, err := (PostgresFallbackPlaybackReplayStore{Pool: pool}).Claim(t.Context(), "session-1", "operation-1", "hash")
		if err == nil || !strings.Contains(err.Error(), "read claimed fallback playback operation") {
			t.Fatalf("Claim() error = %v", err)
		}
	})

	store := PostgresFallbackPlaybackReplayStore{Pool: &fallbackReplayPoolStub{now: time.Now(), records: make(map[fallbackReplayKey]fallbackReplayRecord)}}
	if err := store.Renew(t.Context(), "session-1", "operation-1", "hash", "token"); err == nil {
		t.Fatal("Renew() succeeded for a missing replay")
	}
	if err := store.Complete(t.Context(), "session-1", "operation-1", "hash", "token"); err == nil {
		t.Fatal("Complete() succeeded for a missing replay")
	}
	if err := store.Abort(t.Context(), "session-1", "operation-1", "hash", "token"); err != nil {
		t.Fatalf("Abort() error = %v, want idempotent success for a missing replay", err)
	}
}
