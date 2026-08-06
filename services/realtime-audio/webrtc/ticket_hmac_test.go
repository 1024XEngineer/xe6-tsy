package webrtc

import (
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestHMACTicketValidatorMapsClaims(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	codec := newWebRTCTestTicketCodec(t, now)
	token, err := codec.Issue("session-1", "account-1")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	validator, err := NewHMACTicketValidator(codec)
	if err != nil {
		t.Fatalf("NewHMACTicketValidator() error = %v", err)
	}

	ticket, err := validator.Validate(t.Context(), token, "session-1")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if ticket.SessionID != "session-1" ||
		ticket.AccountID != "account-1" ||
		!ticket.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("ticket = %#v", ticket)
	}
}

func TestHMACTicketValidatorMapsErrors(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	codec := newWebRTCTestTicketCodec(t, now)
	token, err := codec.Issue("session-1", "account-1")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	validator, err := NewHMACTicketValidator(codec)
	if err != nil {
		t.Fatalf("NewHMACTicketValidator() error = %v", err)
	}

	if _, err := validator.Validate(t.Context(), token, "session-2"); !errors.Is(err, ErrTicketSessionMismatch) {
		t.Fatalf("wrong session error = %v, want ErrTicketSessionMismatch", err)
	}
	if _, err := validator.Validate(t.Context(), token+"x", "session-1"); !errors.Is(err, ErrRealtimeTokenRequired) {
		t.Fatalf("tampered token error = %v, want ErrRealtimeTokenRequired", err)
	}
}

func newWebRTCTestTicketCodec(t *testing.T, now time.Time) *realtimev1.HMACTicketCodec {
	t.Helper()
	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		TTL:    time.Minute,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	return codec
}
