package realtimeaccess

import (
	"context"
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
)

func TestTicketSourceIssuesTicketForSessionOwner(t *testing.T) {
	source, err := NewTicketSource(sessionReaderFake{snapshot: sessions.SessionSnapshot{
		SessionID: "session-1",
		AccountID: "account-1",
		Status:    sessions.StatusCreated,
	}}, issuerFake{})
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}

	token, err := source.Token(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if token != "session-1:account-1" {
		t.Fatalf("token = %q", token)
	}
}

func TestTicketSourceTokenForAccountEnforcesOwner(t *testing.T) {
	source, err := NewTicketSource(sessionReaderFake{snapshot: sessions.SessionSnapshot{
		SessionID: "session-1",
		AccountID: "account-1",
		Status:    sessions.StatusCreated,
	}}, issuerFake{})
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}
	token, err := source.TokenForAccount(t.Context(), "account-1", "session-1")
	if err != nil {
		t.Fatalf("TokenForAccount() error = %v", err)
	}
	if token != "session-1:account-1" {
		t.Fatalf("token = %q", token)
	}
	_, err = source.TokenForAccount(t.Context(), "account-2", "session-1")
	if !errors.Is(err, sessions.ErrVoiceSessionNotFound) {
		t.Fatalf("TokenForAccount() error = %v, want ErrVoiceSessionNotFound", err)
	}
}

func TestTicketSourceRejectsInvalidOwnershipFacts(t *testing.T) {
	source, err := NewTicketSource(sessionReaderFake{snapshot: sessions.SessionSnapshot{
		SessionID: "session-2",
		AccountID: "account-1",
		Status:    sessions.StatusCreated,
	}}, issuerFake{})
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}
	_, err = source.Token(t.Context(), "session-1")
	if !errors.Is(err, sessions.ErrInvalidDependency) {
		t.Fatalf("Token() error = %v, want ErrInvalidDependency", err)
	}
}

func TestTicketSourceTokenForAccountRejectsEmptyIDs(t *testing.T) {
	source, err := NewTicketSource(sessionReaderFake{snapshot: sessions.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", Status: sessions.StatusCreated,
	}}, issuerFake{})
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}
	if _, err := source.TokenForAccount(t.Context(), "", "session-1"); !errors.Is(err, sessions.ErrInvalidRequest) {
		t.Fatalf("empty account error = %v", err)
	}
	if _, err := source.TokenForAccount(t.Context(), "account-1", ""); !errors.Is(err, sessions.ErrInvalidRequest) {
		t.Fatalf("empty session error = %v", err)
	}
}

func TestTicketSourceTokenForAccountPropagatesReaderAndIssuerErrors(t *testing.T) {
	want := errors.New("db down")
	source, err := NewTicketSource(sessionReaderFake{err: want}, issuerFake{})
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}
	if _, err := source.TokenForAccount(t.Context(), "account-1", "session-1"); !errors.Is(err, want) {
		t.Fatalf("reader error = %v, want %v", err, want)
	}

	source, err = NewTicketSource(sessionReaderFake{snapshot: sessions.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", Status: sessions.StatusCreated,
	}}, issuerErrorFake{err: want})
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}
	if _, err := source.TokenForAccount(t.Context(), "account-1", "session-1"); !errors.Is(err, want) {
		t.Fatalf("issuer error = %v, want %v", err, want)
	}
}

func TestTicketSourceTokenForAccountRejectsInvalidStatus(t *testing.T) {
	source, err := NewTicketSource(sessionReaderFake{snapshot: sessions.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", Status: sessions.Status("bogus"),
	}}, issuerFake{})
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}
	if _, err := source.TokenForAccount(t.Context(), "account-1", "session-1"); !errors.Is(err, sessions.ErrInvalidDependency) {
		t.Fatalf("error = %v, want ErrInvalidDependency", err)
	}
}

func TestSessionTicketMinterIssuesTicketWithValidatorExpiry(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		TTL:    2 * time.Minute,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	source, err := NewTicketSource(sessionReaderFake{snapshot: sessions.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", Status: sessions.StatusActive,
	}}, codec)
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}
	minter := SessionTicketMinter{Source: source, Validator: codec}
	ticket, err := minter.MintRealtimeTicket(t.Context(), "account-1", "session-1")
	if err != nil {
		t.Fatalf("MintRealtimeTicket() error = %v", err)
	}
	if ticket.SessionID != "session-1" || ticket.Ticket == "" {
		t.Fatalf("ticket = %#v", ticket)
	}
	if !ticket.ExpiresAt.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("ExpiresAt = %v, want %v", ticket.ExpiresAt, now.Add(2*time.Minute))
	}
}

func TestSessionTicketMinterRequiresSource(t *testing.T) {
	_, err := (SessionTicketMinter{}).MintRealtimeTicket(t.Context(), "account-1", "session-1")
	if !errors.Is(err, ErrInvalidDependency) {
		t.Fatalf("error = %v, want ErrInvalidDependency", err)
	}
}

func TestSessionTicketMinterWithoutValidatorUsesFallbackExpiry(t *testing.T) {
	source, err := NewTicketSource(sessionReaderFake{snapshot: sessions.SessionSnapshot{
		SessionID: "session-1", AccountID: "account-1", Status: sessions.StatusCreated,
	}}, issuerFake{})
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}
	before := time.Now().UTC()
	ticket, err := (SessionTicketMinter{Source: source}).MintRealtimeTicket(t.Context(), "account-1", "session-1")
	if err != nil {
		t.Fatalf("MintRealtimeTicket() error = %v", err)
	}
	if ticket.Ticket != "session-1:account-1" {
		t.Fatalf("ticket = %#v", ticket)
	}
	if ticket.ExpiresAt.Before(before.Add(30*time.Second)) || ticket.ExpiresAt.After(before.Add(2*time.Minute)) {
		t.Fatalf("ExpiresAt = %v, want ~1m fallback", ticket.ExpiresAt)
	}
}

func TestTicketSourceWorksWithSharedHMACCodec(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte("0123456789abcdef0123456789abcdef"),
		TTL:    time.Minute,
		Now:    func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	source, err := NewTicketSource(sessionReaderFake{snapshot: sessions.SessionSnapshot{
		SessionID: "session-1",
		AccountID: "account-1",
		Status:    sessions.StatusActive,
	}}, codec)
	if err != nil {
		t.Fatalf("NewTicketSource() error = %v", err)
	}
	token, err := source.Token(t.Context(), "session-1")
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	claims, err := codec.Validate(token, "session-1")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if claims.AccountID != "account-1" {
		t.Fatalf("claims = %#v", claims)
	}
}

type sessionReaderFake struct {
	snapshot sessions.SessionSnapshot
	err      error
}

func (f sessionReaderFake) GetSession(
	_ context.Context,
	_ string,
) (sessions.SessionSnapshot, error) {
	return f.snapshot, f.err
}

type issuerFake struct{}

func (issuerFake) Issue(sessionID string, accountID string) (string, error) {
	return sessionID + ":" + accountID, nil
}

type issuerErrorFake struct{ err error }

func (f issuerErrorFake) Issue(string, string) (string, error) {
	return "", f.err
}
