package webrtc

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

// MemoryConnectionManager is a deterministic, process-local signaling store for the skeleton.
type MemoryConnectionManager struct {
	mu       sync.Mutex
	factory  ConnectionTransportFactory
	sessions map[string]*sessionConnections
	nextID   int64
}

type sessionConnections struct {
	byID             map[string]*connectionRecord
	byIdempotencyKey map[string]string
}

type connectionRecord struct {
	connection      Connection
	transport       ConnectionTransport
	candidateIDs    map[string]struct{}
	endOfCandidates bool
}

// NewMemoryConnectionManager creates an empty manager with session-isolated connection state.
func NewMemoryConnectionManager(factory ConnectionTransportFactory) *MemoryConnectionManager {
	return &MemoryConnectionManager{
		factory:  factory,
		sessions: make(map[string]*sessionConnections),
	}
}

// Open reserves one idempotency key while the manager creates and retains its transport handle.
func (m *MemoryConnectionManager) Open(ctx context.Context, request OpenConnectionRequest) (Connection, error) {
	if err := ctx.Err(); err != nil {
		return Connection{}, err
	}
	if err := validateOpenRequest(request); err != nil {
		return Connection{}, err
	}
	if m == nil || m.factory == nil {
		return Connection{}, ErrInvalidDependency
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	connections := m.sessions[request.SessionID]
	if connections == nil {
		connections = &sessionConnections{
			byID: make(map[string]*connectionRecord), byIdempotencyKey: make(map[string]string),
		}
		m.sessions[request.SessionID] = connections
	}
	if existingID, ok := connections.byIdempotencyKey[request.IdempotencyKey]; ok {
		return connections.byID[existingID].connection, nil
	}

	m.nextID++
	connectionID := fmt.Sprintf("rtc_%06d", m.nextID)
	transport, err := m.factory.Create(ctx, request.SessionID, connectionID)
	if err != nil {
		return Connection{}, fmt.Errorf("create WebRTC transport: %w", err)
	}
	answer, err := transport.Answer(ctx, request.Offer)
	if err != nil {
		closeErr := transport.Close(context.WithoutCancel(ctx))
		return Connection{}, errors.Join(fmt.Errorf("create SDP answer: %w", err), closeErr)
	}
	connection := Connection{
		ID:        connectionID,
		SessionID: request.SessionID, IdempotencyKey: request.IdempotencyKey,
		Offer: request.Offer, Answer: answer, State: ConnectionConnecting, CreatedAt: request.CreatedAt,
	}
	connections.byID[connection.ID] = &connectionRecord{connection: connection, transport: transport, candidateIDs: make(map[string]struct{})}
	connections.byIdempotencyKey[connection.IdempotencyKey] = connection.ID
	return connection, nil
}

// AddCandidates records new candidate IDs and reports repeats without duplicating them.
func (m *MemoryConnectionManager) AddCandidates(ctx context.Context, sessionID string, request CandidateRequest) (CandidateResponse, error) {
	if err := ctx.Err(); err != nil {
		return CandidateResponse{}, err
	}
	if sessionID == "" {
		return CandidateResponse{}, ErrSessionIDRequired
	}
	if request.ConnectionID == "" {
		return CandidateResponse{}, ErrConnectionIDRequired
	}
	for _, candidate := range request.Candidates {
		if candidate.ID == "" {
			return CandidateResponse{}, ErrCandidateIDRequired
		}
		if candidate.Candidate == "" {
			return CandidateResponse{}, ErrCandidateRequired
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	connections := m.sessions[sessionID]
	if connections == nil {
		return CandidateResponse{}, ErrConnectionNotFound
	}
	record := connections.byID[request.ConnectionID]
	if record == nil {
		return CandidateResponse{}, ErrConnectionNotFound
	}
	if record.connection.SessionID != sessionID {
		return CandidateResponse{}, ErrConnectionSessionMismatch
	}

	response := CandidateResponse{ConnectionID: request.ConnectionID}
	for _, candidate := range request.Candidates {
		if _, exists := record.candidateIDs[candidate.ID]; exists {
			response.DeduplicatedCandidateIDs = append(response.DeduplicatedCandidateIDs, candidate.ID)
			continue
		}
		if err := record.transport.AddCandidate(ctx, candidate); err != nil {
			return CandidateResponse{}, fmt.Errorf("apply ICE candidate: %w", err)
		}
		record.candidateIDs[candidate.ID] = struct{}{}
		response.AcceptedCandidateIDs = append(response.AcceptedCandidateIDs, candidate.ID)
	}
	if request.EndOfCandidates {
		record.endOfCandidates = true
	}
	response.EndOfCandidates = record.endOfCandidates
	return response, nil
}

// Close releases every in-memory connection for a session and remains successful when no connection exists.
func (m *MemoryConnectionManager) Close(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sessionID == "" {
		return ErrSessionIDRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	connections := m.sessions[sessionID]
	if connections == nil {
		return nil
	}
	var closeErr error
	for _, record := range connections.byID {
		closeErr = errors.Join(closeErr, record.transport.Close(ctx))
	}
	if closeErr != nil {
		return closeErr
	}
	delete(m.sessions, sessionID)
	return nil
}

func validateOpenRequest(request OpenConnectionRequest) error {
	switch {
	case request.SessionID == "":
		return ErrSessionIDRequired
	case request.IdempotencyKey == "":
		return ErrIdempotencyKeyRequired
	case request.Offer.SDP == "":
		return ErrOfferSDPRequired
	case request.Offer.Type != "offer":
		return ErrOfferTypeInvalid
	case request.CreatedAt.IsZero():
		return ErrInvalidDependency
	}
	return nil
}

var _ ConnectionManager = (*MemoryConnectionManager)(nil)
var _ session.WebRTCConnectionManager = (*MemoryConnectionManager)(nil)
