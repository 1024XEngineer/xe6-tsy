package realtimeaccess

import (
	"context"
	"fmt"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
)

type ticketIssuer interface {
	Issue(sessionID string, accountID string) (string, error)
}

type ticketValidator interface {
	Validate(token string, sessionID string) (realtimev1.TicketClaims, error)
}

type TicketSource struct {
	reader sessions.SessionReader
	issuer ticketIssuer
}

func NewTicketSource(reader sessions.SessionReader, issuer ticketIssuer) (*TicketSource, error) {
	if reader == nil || issuer == nil {
		return nil, ErrInvalidDependency
	}
	return &TicketSource{reader: reader, issuer: issuer}, nil
}

// Token issues a realtime ticket for a known session (server-side control plane).
func (s *TicketSource) Token(ctx context.Context, sessionID string) (string, error) {
	if sessionID == "" {
		return "", sessions.ErrInvalidRequest
	}
	session, err := s.reader.GetSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if session.SessionID != sessionID || session.AccountID == "" || !session.Status.Valid() {
		return "", sessions.ErrInvalidDependency
	}
	token, err := s.issuer.Issue(session.SessionID, session.AccountID)
	if err != nil {
		return "", fmt.Errorf("issue realtime ticket: %w", err)
	}
	return token, nil
}

// TokenForAccount issues a ticket only when the caller owns the session.
func (s *TicketSource) TokenForAccount(ctx context.Context, accountID, sessionID string) (string, error) {
	if accountID == "" || sessionID == "" {
		return "", sessions.ErrInvalidRequest
	}
	session, err := s.reader.GetSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if session.SessionID != sessionID || session.AccountID == "" || !session.Status.Valid() {
		return "", sessions.ErrInvalidDependency
	}
	if session.AccountID != accountID {
		return "", sessions.ErrVoiceSessionNotFound
	}
	token, err := s.issuer.Issue(session.SessionID, session.AccountID)
	if err != nil {
		return "", fmt.Errorf("issue realtime ticket: %w", err)
	}
	return token, nil
}

// SessionTicketMinter adapts TicketSource to the sessions HTTP mint boundary.
type SessionTicketMinter struct {
	Source    *TicketSource
	Validator ticketValidator
}

func (m SessionTicketMinter) MintRealtimeTicket(
	ctx context.Context,
	accountID, sessionID string,
) (sessions.RealtimeTicket, error) {
	if m.Source == nil {
		return sessions.RealtimeTicket{}, ErrInvalidDependency
	}
	token, err := m.Source.TokenForAccount(ctx, accountID, sessionID)
	if err != nil {
		return sessions.RealtimeTicket{}, err
	}
	expiresAt := time.Now().UTC().Add(time.Minute)
	if m.Validator != nil {
		claims, err := m.Validator.Validate(token, sessionID)
		if err != nil {
			return sessions.RealtimeTicket{}, fmt.Errorf("issued ticket failed validation: %w", err)
		}
		expiresAt = claims.ExpiresAt
	}
	return sessions.RealtimeTicket{
		Ticket:    token,
		SessionID: sessionID,
		ExpiresAt: expiresAt,
	}, nil
}
