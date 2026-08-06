package realtimev1

import (
	"errors"
	"testing"
	"time"
)

func TestHMACTicketCodecSignsSessionScopedTickets(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	codec := newTestTicketCodec(t, now)

	token, err := codec.Issue("session-1", "account-1")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	claims, err := codec.Validate(token, "session-1")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if claims.SessionID != "session-1" ||
		claims.AccountID != "account-1" ||
		!claims.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("claims = %#v", claims)
	}
}

func TestHMACTicketCodecRejectsInvalidTickets(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	codec := newTestTicketCodec(t, now)
	token, err := codec.Issue("session-1", "account-1")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	tests := []struct {
		name  string
		token string
		want  error
	}{
		{name: "tampered", token: token + "x", want: ErrTicketInvalid},
		{name: "wrong session", token: token, want: ErrTicketSessionMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessionID := "session-1"
			if test.name == "wrong session" {
				sessionID = "session-2"
			}
			_, err := codec.Validate(test.token, sessionID)
			if !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestHMACTicketCodecRejectsExpiredTickets(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	current := now
	codec, err := NewHMACTicketCodec(TicketConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		TTL:    time.Minute,
		Now:    func() time.Time { return current },
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	token, err := codec.Issue("session-1", "account-1")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	current = now.Add(time.Minute)
	if _, err := codec.Validate(token, "session-1"); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("Validate() error = %v, want ErrTicketExpired", err)
	}
}

func TestHMACTicketCodecValidatesConfigAndInputs(t *testing.T) {
	if _, err := NewHMACTicketCodec(TicketConfig{Secret: []byte("short")}); !errors.Is(err, ErrTicketConfig) {
		t.Fatalf("NewHMACTicketCodec() error = %v, want ErrTicketConfig", err)
	}
	codec := newTestTicketCodec(t, time.Unix(1700000000, 0).UTC())
	if _, err := codec.Issue("", "account-1"); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("Issue(empty session) error = %v, want ErrTicketInvalid", err)
	}
	if _, err := codec.Validate("", "session-1"); !errors.Is(err, ErrTicketInvalid) {
		t.Fatalf("Validate(empty token) error = %v, want ErrTicketInvalid", err)
	}
}

func newTestTicketCodec(t *testing.T, now time.Time) *HMACTicketCodec {
	t.Helper()
	codec, err := NewHMACTicketCodec(TicketConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		TTL:    time.Minute,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	return codec
}
