package webrtc

import (
	"context"
	"fmt"
	"time"
)

const (
	defaultDataChannelLabel = "translation-events"
	defaultTTSTrackID       = "tts-audio"
)

// Dependencies contains the external authorization, SDP, and connection boundaries used by signaling.
type Dependencies struct {
	Tickets     TicketValidator
	Answers     SDPAnswerer
	Connections ConnectionManager
	Now         func() time.Time
}

// SignalingService coordinates authorized offer and candidate commands without owning a PeerConnection implementation.
type SignalingService struct {
	tickets     TicketValidator
	answers     SDPAnswerer
	connections ConnectionManager
	now         func() time.Time
}

// NewSignalingService validates the required boundaries before accepting signaling commands.
func NewSignalingService(dependencies Dependencies) (*SignalingService, error) {
	if dependencies.Tickets == nil || dependencies.Answers == nil || dependencies.Connections == nil {
		return nil, ErrInvalidDependency
	}
	if dependencies.Now == nil {
		dependencies.Now = func() time.Time { return time.Now().UTC() }
	}
	return &SignalingService{
		tickets: dependencies.Tickets, answers: dependencies.Answers,
		connections: dependencies.Connections, now: dependencies.Now,
	}, nil
}

// Offer validates a short-lived ticket, obtains an SDP answer, and reserves an idempotent connection record.
func (s *SignalingService) Offer(ctx context.Context, token, sessionID string, request OfferRequest) (OfferResponse, error) {
	if err := ctx.Err(); err != nil {
		return OfferResponse{}, err
	}
	if err := validateOfferRequest(sessionID, token, request); err != nil {
		return OfferResponse{}, err
	}
	if err := s.validateTicket(ctx, token, sessionID); err != nil {
		return OfferResponse{}, err
	}
	existing, found, err := s.connections.Find(ctx, sessionID, request.IdempotencyKey)
	if err != nil {
		return OfferResponse{}, fmt.Errorf("find WebRTC connection: %w", err)
	}
	if found {
		return offerResponse(existing), nil
	}

	offer := SessionDescription{SDP: request.SDP, Type: request.Type}
	answer, err := s.answers.Answer(ctx, offer)
	if err != nil {
		return OfferResponse{}, fmt.Errorf("create SDP answer: %w", err)
	}
	connection, err := s.connections.Open(ctx, OpenConnectionRequest{
		SessionID: sessionID, IdempotencyKey: request.IdempotencyKey,
		Offer: offer, Answer: answer, CreatedAt: s.now(),
	})
	if err != nil {
		return OfferResponse{}, fmt.Errorf("open WebRTC connection: %w", err)
	}
	return offerResponse(connection), nil
}

func offerResponse(connection Connection) OfferResponse {
	return OfferResponse{
		SDP: connection.Answer.SDP, Type: connection.Answer.Type,
		SessionID: connection.SessionID, ConnectionID: connection.ID,
		DataChannelLabel: defaultDataChannelLabel, TTSTrackID: defaultTTSTrackID,
		ConnectionState: connection.State,
	}
}

// AddCandidates accepts candidates only after the same session ticket boundary has been validated.
func (s *SignalingService) AddCandidates(ctx context.Context, token, sessionID string, request CandidateRequest) (CandidateResponse, error) {
	if err := ctx.Err(); err != nil {
		return CandidateResponse{}, err
	}
	if sessionID == "" {
		return CandidateResponse{}, ErrSessionIDRequired
	}
	if token == "" {
		return CandidateResponse{}, ErrRealtimeTokenRequired
	}
	if err := s.validateTicket(ctx, token, sessionID); err != nil {
		return CandidateResponse{}, err
	}
	response, err := s.connections.AddCandidates(ctx, sessionID, request)
	if err != nil {
		return CandidateResponse{}, fmt.Errorf("add ICE candidates: %w", err)
	}
	return response, nil
}

func (s *SignalingService) validateTicket(ctx context.Context, token, sessionID string) error {
	ticket, err := s.tickets.Validate(ctx, token, sessionID)
	if err != nil {
		return fmt.Errorf("validate realtime ticket: %w", err)
	}
	if ticket.SessionID != sessionID {
		return ErrTicketSessionMismatch
	}
	if ticket.AccountID == "" {
		return ErrTicketAccountRequired
	}
	if ticket.ExpiresAt.IsZero() || !ticket.ExpiresAt.After(s.now()) {
		return ErrTicketExpired
	}
	return nil
}

func validateOfferRequest(sessionID, token string, request OfferRequest) error {
	switch {
	case sessionID == "":
		return ErrSessionIDRequired
	case token == "":
		return ErrRealtimeTokenRequired
	case request.IdempotencyKey == "":
		return ErrIdempotencyKeyRequired
	case request.SDP == "":
		return ErrOfferSDPRequired
	case request.Type != "offer":
		return ErrOfferTypeInvalid
	}
	return nil
}
