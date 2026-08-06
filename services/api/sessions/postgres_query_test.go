package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestSessionQueryBoundariesKeepActorAndOwnerDistinct(t *testing.T) {
	now := time.Date(2026, time.July, 30, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		read          func(context.Context, queryRower) (VoiceSession, error)
		wantSQL       string
		rejectSQL     string
		wantArgs      []any
		wantForUpdate bool
	}{
		{
			name: "actor lineage",
			read: func(ctx context.Context, db queryRower) (VoiceSession, error) {
				return getSessionForActor(
					ctx, db, "acct_actor", "session-1", true,
				)
			},
			wantSQL:       "lingow_account_lineage($2)",
			wantArgs:      []any{"session-1", "acct_actor"},
			wantForUpdate: true,
		},
		{
			name: "exact owner",
			read: func(ctx context.Context, db queryRower) (VoiceSession, error) {
				return getSessionByOwner(
					ctx, db, "acct_owner", "session-1", false,
				)
			},
			wantSQL:   "account_id = $2",
			rejectSQL: "lingow_account_lineage",
			wantArgs:  []any{"session-1", "acct_owner"},
		},
		{
			name: "trusted internal",
			read: func(ctx context.Context, db queryRower) (VoiceSession, error) {
				return getSessionTrusted(ctx, db, "session-1", false)
			},
			wantSQL:   "WHERE id = $1",
			rejectSQL: "AND account_id",
			wantArgs:  []any{"session-1"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &recordingSessionQuery{
				row: sessionRow{
					sessionID: "session-1",
					ownerID:   "acct_owner",
					status:    string(StatusCreated),
					createdAt: now,
				},
			}
			session, err := test.read(t.Context(), db)
			if err != nil {
				t.Fatalf("read session: %v", err)
			}
			if session.AccountID != "acct_owner" {
				t.Fatalf("session owner = %q, want acct_owner", session.AccountID)
			}
			if !strings.Contains(db.query, test.wantSQL) {
				t.Fatalf("query = %q, missing %q", db.query, test.wantSQL)
			}
			if test.rejectSQL != "" && strings.Contains(db.query, test.rejectSQL) {
				t.Fatalf("query = %q, unexpectedly contains %q", db.query, test.rejectSQL)
			}
			if got := strings.HasSuffix(strings.TrimSpace(db.query), "FOR UPDATE"); got != test.wantForUpdate {
				t.Fatalf("query FOR UPDATE = %t, want %t", got, test.wantForUpdate)
			}
			if len(db.args) != len(test.wantArgs) {
				t.Fatalf("query args = %#v, want %#v", db.args, test.wantArgs)
			}
			for index := range test.wantArgs {
				if db.args[index] != test.wantArgs[index] {
					t.Fatalf("query args = %#v, want %#v", db.args, test.wantArgs)
				}
			}
		})
	}
}

func TestSessionQueryRejectsInvalidPersistedStatus(t *testing.T) {
	db := &recordingSessionQuery{
		row: sessionRow{
			sessionID: "session-1",
			ownerID:   "acct_owner",
			status:    "unknown",
			createdAt: time.Now().UTC(),
		},
	}
	if _, err := getSessionByOwner(
		t.Context(), db, "acct_owner", "session-1", false,
	); err == nil || !strings.Contains(err.Error(), "invalid persisted session status") {
		t.Fatalf("getSessionByOwner() error = %v", err)
	}
}

func TestSessionQueryPropagatesScanError(t *testing.T) {
	want := errors.New("scan failed")
	db := &recordingSessionQuery{row: sessionRow{err: want}}
	if _, err := getSessionTrusted(
		t.Context(), db, "session-1", false,
	); !errors.Is(err, want) {
		t.Fatalf("getSessionTrusted() error = %v, want %v", err, want)
	}
}

type recordingSessionQuery struct {
	query string
	args  []any
	row   sessionRow
}

func (q *recordingSessionQuery) QueryRow(
	_ context.Context,
	query string,
	args ...any,
) pgx.Row {
	q.query = query
	q.args = append([]any(nil), args...)
	return q.row
}

type sessionRow struct {
	sessionID string
	ownerID   string
	status    string
	createdAt time.Time
	err       error
}

func (r sessionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 8 {
		return errors.New("unexpected session scan destination count")
	}
	*(dest[0].(*string)) = r.sessionID
	*(dest[1].(*string)) = r.ownerID
	*(dest[2].(*string)) = r.status
	*(dest[3].(*json.RawMessage)) = json.RawMessage(`{"codec":"opus"}`)
	*(dest[4].(*json.RawMessage)) = json.RawMessage(`{"webrtc":true}`)
	*(dest[5].(**time.Time)) = nil
	*(dest[6].(**time.Time)) = nil
	*(dest[7].(*time.Time)) = r.createdAt
	return nil
}
