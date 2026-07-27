package webrtc

import (
	"context"
	"fmt"
	"sync"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

// MemoryConnectionManager is a deterministic, process-local signaling store for the skeleton.
type MemoryConnectionManager struct {
	mu        sync.Mutex
	sessions  map[string]*sessionConnections
	sequences map[string]int64
}

type sessionConnections struct {
	byID             map[string]*connectionRecord
	byIdempotencyKey map[string]string
}

type connectionRecord struct {
	connection      Connection
	candidateIDs    map[string]struct{}
	endOfCandidates bool
}

// NewMemoryConnectionManager creates an empty manager with session-isolated connection state.
func NewMemoryConnectionManager() *MemoryConnectionManager {
	return &MemoryConnectionManager{
		sessions:  make(map[string]*sessionConnections),
		sequences: make(map[string]int64),
	}
}

// Find returns the connection retained for a session-local offer idempotency key.
func (m *MemoryConnectionManager) Find(ctx context.Context, sessionID, idempotencyKey string) (Connection, bool, error) {
	if err := ctx.Err(); err != nil {
		return Connection{}, false, err
	}
	if sessionID == "" {
		return Connection{}, false, ErrSessionIDRequired
	}
	if idempotencyKey == "" {
		return Connection{}, false, ErrIdempotencyKeyRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	connections := m.sessions[sessionID]
	if connections == nil {
		return Connection{}, false, nil
	}
	connectionID, found := connections.byIdempotencyKey[idempotencyKey]
	if !found {
		return Connection{}, false, nil
	}
	return connections.byID[connectionID].connection, true, nil
}

// Open creates one connecting record or returns the record retained for the same idempotency key.
func (m *MemoryConnectionManager) Open(ctx context.Context, request OpenConnectionRequest) (Connection, error) {
	if err := ctx.Err(); err != nil {
		return Connection{}, err
	}
	if err := validateOpenRequest(request); err != nil {
		return Connection{}, err
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

	m.sequences[request.SessionID]++
	connection := Connection{
		ID:        fmt.Sprintf("rtc_%s_%06d", request.SessionID, m.sequences[request.SessionID]),
		SessionID: request.SessionID, IdempotencyKey: request.IdempotencyKey,
		Offer: request.Offer, Answer: request.Answer, State: ConnectionConnecting, CreatedAt: request.CreatedAt,
	}
	connections.byID[connection.ID] = &connectionRecord{connection: connection, candidateIDs: make(map[string]struct{})}
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
	delete(m.sessions, sessionID)
	return nil
}

func validateOpenRequest(request OpenConnectionRequest) error {
	switch {
	case request.SessionID == "":
		return ErrSessionIDRequired
	case request.IdempotencyKey == "":
		return ErrIdempotencyKeyRequired
	case request.Offer.SDP == "" || request.Answer.SDP == "":
		return ErrOfferSDPRequired
	case request.Offer.Type != "offer" || request.Answer.Type != "answer":
		return ErrOfferTypeInvalid
	case request.CreatedAt.IsZero():
		return ErrInvalidDependency
	}
	return nil
}

var _ ConnectionManager = (*MemoryConnectionManager)(nil)
var _ session.WebRTCConnectionManager = (*MemoryConnectionManager)(nil)
