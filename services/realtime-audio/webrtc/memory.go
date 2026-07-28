package webrtc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
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
	currentID        string
	byID             map[string]*connectionRecord
	byIdempotencyKey map[string]string
}

type connectionRecord struct {
	connection      Connection
	snapshot        realtimev1.ConnectionSnapshot
	transport       ConnectionTransport
	candidateIDs    map[string]ICECandidate
	endOfCandidates bool
	opening         bool
	openDone        chan struct{}
	openErr         error
}

type transportStateGate struct {
	mu       sync.Mutex
	delivery sync.Mutex
	ready    bool
	rejected bool
	pending  []transportStateUpdate
	apply    ConnectionStateHandler
}

type transportStateUpdate struct {
	state     realtimev1.ConnectionState
	updatedAt time.Time
}

func newTransportStateGate(apply ConnectionStateHandler) *transportStateGate {
	return &transportStateGate{apply: apply}
}

func (g *transportStateGate) Notify(state realtimev1.ConnectionState, updatedAt time.Time) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.rejected {
		g.mu.Unlock()
		return
	}
	if !g.ready {
		g.pending = append(g.pending, transportStateUpdate{state: state, updatedAt: updatedAt})
		g.mu.Unlock()
		return
	}
	apply := g.apply
	g.mu.Unlock()
	if apply != nil {
		g.delivery.Lock()
		defer g.delivery.Unlock()
		apply(state, updatedAt)
	}
}

func (g *transportStateGate) Activate() {
	if g == nil {
		return
	}
	g.delivery.Lock()
	defer g.delivery.Unlock()
	g.mu.Lock()
	if g.rejected {
		g.mu.Unlock()
		return
	}
	g.ready = true
	pending := append([]transportStateUpdate(nil), g.pending...)
	g.pending = nil
	apply := g.apply
	g.mu.Unlock()
	if apply == nil {
		return
	}
	for _, update := range pending {
		apply(update.state, update.updatedAt)
	}
}

func (g *transportStateGate) Reject() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.rejected = true
	g.pending = nil
	g.mu.Unlock()
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
		if existing.opening {
			done := existing.openDone
			connections.mu.Unlock()
			select {
			case <-ctx.Done():
				return Connection{}, ctx.Err()
			case <-done:
				return completedOpenResult(connections, existing)
			}
		}
		connection, openErr := existing.connection, existing.openErr
		connections.mu.Unlock()
		return connection, openErr
	}
	if connections.currentID != "" {
		connections.mu.Unlock()
		return Connection{}, ErrConnectionAlreadyExists
	}

	connectionID := m.nextConnectionID()
	stateGate := newTransportStateGate(func(state realtimev1.ConnectionState, updatedAt time.Time) {
		_, _ = m.ApplyState(context.Background(), request.SessionID, connectionID, state, updatedAt)
	})
	connection := Connection{
		ID:        connectionID,
		SessionID: request.SessionID, IdempotencyKey: request.IdempotencyKey,
		Offer: request.Offer, State: realtimev1.ConnectionConnecting, CreatedAt: request.CreatedAt,
	}
	record := &connectionRecord{
		connection: connection,
		snapshot: realtimev1.ConnectionSnapshot{
			SessionID: connection.SessionID, ConnectionID: connection.ID,
			State: connection.State, Version: 1, UpdatedAt: request.CreatedAt,
		},
		candidateIDs: make(map[string]ICECandidate), opening: true, openDone: make(chan struct{}),
	}
	connections.byID[connection.ID] = record
	connections.currentID = connection.ID
	connections.byIdempotencyKey[connection.IdempotencyKey] = connection.ID
	connections.mu.Unlock()

	transport, err := m.factory.Create(ctx, request.SessionID, connectionID, stateGate.Notify)
	if err != nil {
		stateGate.Reject()
		openErr := fmt.Errorf("create WebRTC transport: %w", err)
		return Connection{}, completeOpenFailure(connections, record, openErr)
	}
	if transport == nil {
		stateGate.Reject()
		openErr := fmt.Errorf("create WebRTC transport: %w", ErrTransportRequired)
		return Connection{}, completeOpenFailure(connections, record, openErr)
	}
	if !attachOpenTransport(connections, record, transport) {
		stateGate.Reject()
		closeErr := transport.Close(context.WithoutCancel(ctx))
		return Connection{}, errors.Join(completedOpenError(connections, record), closeErr)
	}
	answer, err := transport.Answer(ctx, request.Offer)
	if err != nil {
		stateGate.Reject()
		openErr := fmt.Errorf("create SDP answer: %w", err)
		openErr, closeTransport := completeOpenFailureWithOwnership(connections, record, openErr)
		if closeTransport {
			openErr = errors.Join(openErr, transport.Close(context.WithoutCancel(ctx)))
		}
		return Connection{}, openErr
	}
	if err := validateAnswer(answer); err != nil {
		stateGate.Reject()
		openErr := fmt.Errorf("validate SDP answer: %w", err)
		openErr, closeTransport := completeOpenFailureWithOwnership(connections, record, openErr)
		if closeTransport {
			openErr = errors.Join(openErr, transport.Close(context.WithoutCancel(ctx)))
		}
		return Connection{}, openErr
	}
	if err := completeOpenSuccess(connections, record, answer); err != nil {
		stateGate.Reject()
		return Connection{}, err
	}
	stateGate.Activate()
	connection, err = completedOpenResult(connections, record)
	if err != nil {
		return Connection{}, err
	}
	return connection, nil
}

func attachOpenTransport(connections *sessionConnections, record *connectionRecord, transport ConnectionTransport) bool {
	connections.mu.Lock()
	defer connections.mu.Unlock()
	if connections.closed || connections.byID[record.connection.ID] != record {
		return false
	}
	record.transport = transport
	return true
}

func completeOpenFailure(connections *sessionConnections, record *connectionRecord, openErr error) error {
	result, _ := completeOpenFailureWithOwnership(connections, record, openErr)
	return result
}

func completeOpenFailureWithOwnership(connections *sessionConnections, record *connectionRecord, openErr error) (error, bool) {
	connections.mu.Lock()
	defer connections.mu.Unlock()
	if !record.opening {
		return record.openErr, false
	}
	removeReservedConnection(connections, record)
	completeOpening(record, openErr)
	return openErr, true
}

func completeOpenSuccess(connections *sessionConnections, record *connectionRecord, answer SessionDescription) error {
	connections.mu.Lock()
	defer connections.mu.Unlock()
	if connections.closed || connections.byID[record.connection.ID] != record {
		if record.opening {
			completeOpening(record, ErrConnectionClosing)
		}
		return record.openErr
	}
	record.connection.Answer = answer
	completeOpening(record, nil)
	return nil
}

func completeOpening(record *connectionRecord, openErr error) {
	if record == nil || !record.opening {
		return
	}
	record.openErr = openErr
	record.opening = false
	close(record.openDone)
}

func completedOpenResult(connections *sessionConnections, record *connectionRecord) (Connection, error) {
	connections.mu.Lock()
	defer connections.mu.Unlock()
	if record.openErr == nil && (connections.closed || connections.byID[record.connection.ID] != record) {
		return record.connection, ErrConnectionClosing
	}
	return record.connection, record.openErr
}

func completedOpenError(connections *sessionConnections, record *connectionRecord) error {
	_, err := completedOpenResult(connections, record)
	if err == nil {
		return ErrConnectionClosing
	}
	return err
}

func removeReservedConnection(connections *sessionConnections, record *connectionRecord) {
	delete(connections.byID, record.connection.ID)
	delete(connections.byIdempotencyKey, record.connection.IdempotencyKey)
	if connections.currentID == record.connection.ID {
		connections.currentID = ""
	}
}

// GetCurrent returns the latest connection generation retained for a session.
func (m *MemoryConnectionManager) GetCurrent(ctx context.Context, sessionID string) (realtimev1.ConnectionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return realtimev1.ConnectionSnapshot{}, err
	}
	if sessionID == "" {
		return realtimev1.ConnectionSnapshot{}, ErrSessionIDRequired
	}
	connections := m.getSession(sessionID)
	if connections == nil {
		return realtimev1.ConnectionSnapshot{}, ErrConnectionNotFound
	}
	connections.mu.Lock()
	defer connections.mu.Unlock()
	record := connections.byID[connections.currentID]
	if connections.closed || record == nil {
		return realtimev1.ConnectionSnapshot{}, ErrConnectionNotFound
	}
	return record.snapshot, nil
}

// ApplyState accepts a transport callback only for the session's current connection generation.
func (m *MemoryConnectionManager) ApplyState(
	ctx context.Context,
	sessionID, connectionID string,
	state realtimev1.ConnectionState,
	updatedAt time.Time,
) (realtimev1.ConnectionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return realtimev1.ConnectionSnapshot{}, err
	}
	switch {
	case sessionID == "":
		return realtimev1.ConnectionSnapshot{}, ErrSessionIDRequired
	case connectionID == "":
		return realtimev1.ConnectionSnapshot{}, ErrConnectionIDRequired
	case !state.Valid():
		return realtimev1.ConnectionSnapshot{}, ErrConnectionStateInvalid
	case updatedAt.IsZero():
		return realtimev1.ConnectionSnapshot{}, ErrConnectionStateTimeRequired
	}

	connections := m.getSession(sessionID)
	if connections == nil {
		return realtimev1.ConnectionSnapshot{}, ErrConnectionNotFound
	}
	connections.mu.Lock()
	defer connections.mu.Unlock()
	if connections.closed || connections.currentID != connectionID {
		return realtimev1.ConnectionSnapshot{}, ErrConnectionNotFound
	}
	record := connections.byID[connectionID]
	if record == nil {
		return realtimev1.ConnectionSnapshot{}, ErrConnectionNotFound
	}
	if !updatedAt.After(record.snapshot.UpdatedAt) {
		return realtimev1.ConnectionSnapshot{}, ErrConnectionStateStale
	}
	if record.snapshot.State == state {
		record.snapshot.UpdatedAt = updatedAt
		return record.snapshot, nil
	}
	if !validConnectionStateTransition(record.snapshot.State, state) {
		return realtimev1.ConnectionSnapshot{}, ErrConnectionStateTransition
	}
	record.connection.State = state
	record.snapshot.State = state
	record.snapshot.Version++
	record.snapshot.UpdatedAt = updatedAt
	return record.snapshot, nil
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
		if record.transport != nil {
			if err := record.transport.Close(ctx); err != nil {
				closeErr = errors.Join(closeErr, err)
				continue
			}
		}
		completeOpening(record, ErrConnectionClosing)
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

func validateAnswer(answer SessionDescription) error {
	switch {
	case answer.SDP == "":
		return ErrAnswerSDPRequired
	case answer.Type != "answer":
		return ErrAnswerTypeInvalid
	}
	return nil
}

var _ ConnectionManager = (*MemoryConnectionManager)(nil)
var _ session.WebRTCConnectionManager = (*MemoryConnectionManager)(nil)
