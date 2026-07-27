package webrtc

import "context"

// TicketValidator validates API-issued, short-lived realtime tickets at the service boundary.
type TicketValidator interface {
	Validate(ctx context.Context, token, sessionID string) (ConnectionTicket, error)
}

// SDPAnswerer isolates future PeerConnection implementations from signaling orchestration.
type SDPAnswerer interface {
	Answer(ctx context.Context, offer SessionDescription) (SessionDescription, error)
}

// ConnectionManager owns connection metadata and idempotent candidate acceptance for one realtime process.
type ConnectionManager interface {
	Find(ctx context.Context, sessionID, idempotencyKey string) (Connection, bool, error)
	Open(ctx context.Context, request OpenConnectionRequest) (Connection, error)
	AddCandidates(ctx context.Context, sessionID string, request CandidateRequest) (CandidateResponse, error)
	Close(ctx context.Context, sessionID string) error
}
