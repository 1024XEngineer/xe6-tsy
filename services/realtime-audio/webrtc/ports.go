package webrtc

import (
	"context"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// TicketValidator validates API-issued, short-lived realtime tickets at the service boundary.
type TicketValidator interface {
	Validate(ctx context.Context, token, sessionID string) (ConnectionTicket, error)
}

// ConnectionTransport is the lifecycle-owned handle for one future PeerConnection.
type ConnectionTransport interface {
	Answer(ctx context.Context, offer SessionDescription) (SessionDescription, error)
	AddCandidate(ctx context.Context, candidate ICECandidate) error
	EndCandidates(ctx context.Context) error
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
	GetCurrent(ctx context.Context, sessionID string) (realtimev1.ConnectionSnapshot, error)
	ApplyState(ctx context.Context, sessionID, connectionID string, state realtimev1.ConnectionState, updatedAt time.Time) (realtimev1.ConnectionSnapshot, error)
	// Close removes a successful connection immediately and does not publish a queryable closed snapshot.
	Close(ctx context.Context, sessionID string) error
}
