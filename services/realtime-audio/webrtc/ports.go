package webrtc

import "context"

// TicketValidator validates API-issued, short-lived realtime tickets at the service boundary.
type TicketValidator interface {
	Validate(ctx context.Context, token, sessionID string) (ConnectionTicket, error)
}

// ConnectionTransport is the lifecycle-owned handle for one future PeerConnection.
type ConnectionTransport interface {
	Answer(ctx context.Context, offer SessionDescription) (SessionDescription, error)
	AddCandidate(ctx context.Context, candidate ICECandidate) error
	Close(ctx context.Context) error
}

// ConnectionTransportFactory creates session-bound transport handles for a connection manager.
type ConnectionTransportFactory interface {
	Create(ctx context.Context, sessionID, connectionID string) (ConnectionTransport, error)
}

// ConnectionManager owns connection metadata, transport handles, and idempotent candidate acceptance.
type ConnectionManager interface {
	Open(ctx context.Context, request OpenConnectionRequest) (Connection, error)
	AddCandidates(ctx context.Context, sessionID string, request CandidateRequest) (CandidateResponse, error)
	Close(ctx context.Context, sessionID string) error
}
