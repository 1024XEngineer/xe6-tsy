package sessions

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestSessionListCursorRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, time.July, 29, 10, 11, 12, 123, time.UTC)
	encoded, err := encodeSessionListCursor(createdAt, "vs_01")
	if err != nil {
		t.Fatalf("encodeSessionListCursor() error = %v", err)
	}
	cursor, err := decodeSessionListCursor(encoded)
	if err != nil {
		t.Fatalf("decodeSessionListCursor() error = %v", err)
	}
	if cursor.Version != sessionListCursorVersion ||
		cursor.ID != "vs_01" ||
		!cursor.CreatedAt.Equal(createdAt) {
		t.Fatalf("decoded cursor = %#v", cursor)
	}
}

func TestSessionListCursorRejectsInvalidValues(t *testing.T) {
	tests := []string{
		"",
		"not-base64!",
		"e30",
		"eyJ2IjoyLCJjcmVhdGVkX2F0IjoiMjAyNi0wNy0yOVQxMDowMDowMFoiLCJpZCI6InZzXzAxIn0",
	}
	for _, encoded := range tests {
		t.Run(encoded, func(t *testing.T) {
			if _, err := decodeSessionListCursor(encoded); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("decodeSessionListCursor(%q) error = %v", encoded, err)
			}
		})
	}
}

func TestPostgresRepositoryRejectsNilPool(t *testing.T) {
	repository := NewPostgresRepository(nil)
	if _, err := repository.GetOwned(t.Context(), "acct_1", "vs_1"); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("GetOwned() error = %v, want ErrInvalidDependency", err)
	}
	if _, _, err := repository.SaveEndIntent(
		t.Context(),
		EndIntent{},
	); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("SaveEndIntent() error = %v, want ErrInvalidDependency", err)
	}
	if _, err := repository.GetEndIntent(
		t.Context(),
		"acct_1",
		"vs_1",
	); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("GetEndIntent() error = %v, want ErrInvalidDependency", err)
	}
	if err := repository.CompleteEndIntent(
		t.Context(),
		"acct_1",
		"vs_1",
		time.Time{},
	); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("CompleteEndIntent() error = %v, want ErrInvalidDependency", err)
	}
	if _, _, err := repository.ClaimPendingEndIntent(
		t.Context(),
		ClaimEndIntentParams{},
	); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("ClaimPendingEndIntent() error = %v, want ErrInvalidDependency", err)
	}
	if err := repository.RetryClaimedEndIntent(
		t.Context(),
		RetryEndIntentParams{},
	); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("RetryClaimedEndIntent() error = %v, want ErrInvalidDependency", err)
	}
	if err := repository.CompleteClaimedEndIntent(
		t.Context(),
		CompleteClaimedEndIntentParams{},
	); !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("CompleteClaimedEndIntent() error = %v, want ErrInvalidDependency", err)
	}
}

func TestPostgresConstraintName(t *testing.T) {
	err := &pgconn.PgError{ConstraintName: "voice_session_start_operations_key_unique"}
	if got := constraintName(err); got != "voice_session_start_operations_key_unique" {
		t.Fatalf("constraintName() = %q", got)
	}
	if got := constraintName(errors.New("plain error")); got != "" {
		t.Fatalf("constraintName(plain error) = %q", got)
	}
}
