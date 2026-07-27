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
	mu               sync.Mutex
	closeDone        chan struct{}
	closed           bool
	byID             map[string]*connectionRecord
	byIdempotencyKey map[string]string
}

type connectionRecord struct {
	connection      Connection
	transport       ConnectionTransport
	candidateIDs    map[string]ICECandidate
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

	connections, err := m.getOrCreateOpenSession(request.SessionID)
	if err != nil {
		return Connection{}, err
	}
	connections.mu.Lock()
	if connections.closed {
		connections.mu.Unlock()
		return Connection{}, ErrConnectionClosing
	}
	if existingID, ok := connections.byIdempotencyKey[request.IdempotencyKey]; ok {
		existing := connections.byID[existingID]
		if existing.connection.Offer != request.Offer {
			connections.mu.Unlock()
			return Connection{}, ErrIdempotencyPayloadConflict
		}
		connection := existing.connection
		connections.mu.Unlock()
		return connection, nil
	}

	connectionID := m.nextConnectionID()
	transport, err := m.factory.Create(ctx, request.SessionID, connectionID)
	if err != nil {
		connections.mu.Unlock()
		return Connection{}, fmt.Errorf("create WebRTC transport: %w", err)
	}
	answer, err := transport.Answer(ctx, request.Offer)
	if err != nil {
		connections.mu.Unlock()
		closeErr := transport.Close(context.WithoutCancel(ctx))
		return Connection{}, errors.Join(fmt.Errorf("create SDP answer: %w", err), closeErr)
	}
	connection := Connection{
		ID:        connectionID,
		SessionID: request.SessionID, IdempotencyKey: request.IdempotencyKey,
		Offer: request.Offer, Answer: answer, State: ConnectionConnecting, CreatedAt: request.CreatedAt,
	}
	connections.byID[connection.ID] = &connectionRecord{connection: connection, transport: transport, candidateIDs: make(map[string]ICECandidate)}
	connections.byIdempotencyKey[connection.IdempotencyKey] = connection.ID
	connections.mu.Unlock()
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

	connections := m.getSession(sessionID)
	if connections == nil {
		return CandidateResponse{}, ErrConnectionNotFound
	}
	connections.mu.Lock()
	defer connections.mu.Unlock()
	if connections.closed {
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
	if record.endOfCandidates {
		for _, candidate := range request.Candidates {
			previous, exists := record.candidateIDs[candidate.ID]
			if !exists {
				return CandidateResponse{}, ErrCandidatesCompleted
			}
			if !sameICECandidate(previous, candidate) {
				return CandidateResponse{}, ErrIdempotencyPayloadConflict
			}
			response.DeduplicatedCandidateIDs = append(response.DeduplicatedCandidateIDs, candidate.ID)
		}
		response.EndOfCandidates = true
		return response, nil
	}
	for _, candidate := range request.Candidates {
		if previous, exists := record.candidateIDs[candidate.ID]; exists {
			if !sameICECandidate(previous, candidate) {
				return CandidateResponse{}, ErrIdempotencyPayloadConflict
			}
			response.DeduplicatedCandidateIDs = append(response.DeduplicatedCandidateIDs, candidate.ID)
			continue
		}
		if err := record.transport.AddCandidate(ctx, candidate); err != nil {
			return CandidateResponse{}, fmt.Errorf("apply ICE candidate: %w", err)
		}
		record.candidateIDs[candidate.ID] = candidate
		response.AcceptedCandidateIDs = append(response.AcceptedCandidateIDs, candidate.ID)
	}
	if request.EndOfCandidates && !record.endOfCandidates {
		if err := record.transport.EndCandidates(ctx); err != nil {
			return CandidateResponse{}, fmt.Errorf("complete ICE candidates: %w", err)
		}
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

	for {
		connections, waiting := m.beginClose(sessionID)
		if connections == nil {
			return nil
		}
		if waiting != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-waiting:
				continue
			}
		}

		closeErr := closeSession(ctx, connections)
		m.finishClose(sessionID, connections, closeErr)
		return closeErr
	}
}

func closeSession(ctx context.Context, connections *sessionConnections) error {
	connections.mu.Lock()
	defer connections.mu.Unlock()
	connections.closed = true
	var closeErr error
	for connectionID, record := range connections.byID {
		if err := record.transport.Close(ctx); err != nil {
			closeErr = errors.Join(closeErr, err)
			continue
		}
		delete(connections.byID, connectionID)
		delete(connections.byIdempotencyKey, record.connection.IdempotencyKey)
	}
	if closeErr != nil {
		connections.closed = false
		return closeErr
	}
	return nil
}

func (m *MemoryConnectionManager) beginClose(sessionID string) (*sessionConnections, <-chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	connections := m.sessions[sessionID]
	if connections == nil {
		return nil, nil
	}
	if connections.closeDone != nil {
		return connections, connections.closeDone
	}
	connections.closeDone = make(chan struct{})
	return connections, nil
}

func (m *MemoryConnectionManager) finishClose(sessionID string, connections *sessionConnections, closeErr error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	done := connections.closeDone
	if closeErr == nil && m.sessions[sessionID] == connections {
		delete(m.sessions, sessionID)
	} else if m.sessions[sessionID] == connections {
		connections.closeDone = nil
	}
	close(done)
}

func (m *MemoryConnectionManager) getOrCreateOpenSession(sessionID string) (*sessionConnections, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	connections := m.sessions[sessionID]
	if connections != nil && connections.closeDone != nil {
		return nil, ErrConnectionClosing
	}
	if connections == nil {
		connections = &sessionConnections{
			byID: make(map[string]*connectionRecord), byIdempotencyKey: make(map[string]string),
		}
		m.sessions[sessionID] = connections
	}
	return connections, nil
}

func (m *MemoryConnectionManager) getSession(sessionID string) *sessionConnections {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionID]
}

func (m *MemoryConnectionManager) nextConnectionID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	return fmt.Sprintf("rtc_%06d", m.nextID)
}

func sameICECandidate(left, right ICECandidate) bool {
	return left.ID == right.ID &&
		left.Candidate == right.Candidate &&
		sameStringPointer(left.SDPMid, right.SDPMid) &&
		sameUint16Pointer(left.SDPMLineIndex, right.SDPMLineIndex) &&
		sameStringPointer(left.UsernameFragment, right.UsernameFragment)
}

func sameStringPointer(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func sameUint16Pointer(left, right *uint16) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
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
