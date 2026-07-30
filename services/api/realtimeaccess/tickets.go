package realtimeaccess

import (
	"context"
	"fmt"

	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
)

type ticketIssuer interface {
	Issue(sessionID string, accountID string) (string, error)
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
