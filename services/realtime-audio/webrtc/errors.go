package webrtc

import "errors"

var (
	// ErrInvalidDependency reports a signaling service with an incomplete boundary configuration.
	ErrInvalidDependency = errors.New("invalid WebRTC signaling dependency")
	// ErrSessionIDRequired prevents connections without a session ownership key.
	ErrSessionIDRequired = errors.New("session id is required")
	// ErrConnectionIDRequired prevents candidate writes without a connection key.
	ErrConnectionIDRequired = errors.New("connection id is required")
	// ErrIdempotencyKeyRequired prevents repeated offer requests from creating duplicate connections.
	ErrIdempotencyKeyRequired = errors.New("idempotency key is required")
	// ErrIdempotencyPayloadConflict rejects reuse of an idempotency identifier for different content.
	ErrIdempotencyPayloadConflict = errors.New("idempotency payload conflicts with the existing request")
	// ErrOfferSDPRequired rejects offer or answer descriptions without SDP content.
	ErrOfferSDPRequired = errors.New("session description sdp is required")
	// ErrOfferTypeInvalid rejects SDP descriptions with an unexpected type.
	ErrOfferTypeInvalid = errors.New("invalid session description type")
	// ErrTransportRequired rejects a factory result without an owned transport handle.
	ErrTransportRequired = errors.New("WebRTC transport is required")
	// ErrAnswerSDPRequired rejects an adapter answer without SDP content.
	ErrAnswerSDPRequired = errors.New("SDP answer is required")
	// ErrAnswerTypeInvalid rejects an adapter result that is not an SDP answer.
	ErrAnswerTypeInvalid = errors.New("invalid SDP answer type")
	// ErrCandidateIDRequired prevents a candidate from bypassing idempotent delivery.
	ErrCandidateIDRequired = errors.New("candidate id is required")
	// ErrCandidateRequired rejects a candidate record without its SDP candidate value.
	ErrCandidateRequired = errors.New("candidate is required")
	// ErrCandidatesCompleted rejects previously unseen candidates after the remote ICE generation is final.
	ErrCandidatesCompleted = errors.New("ICE candidate collection is complete")
	// ErrConnectionNotFound reports a missing or already closed session connection.
	ErrConnectionNotFound = errors.New("WebRTC connection not found")
	// ErrConnectionClosing rejects offers while the session's current transport generation is closing.
	ErrConnectionClosing = errors.New("WebRTC session connections are closing")
	// ErrConnectionAlreadyExists prevents a second transport from becoming current for the same session.
	ErrConnectionAlreadyExists = errors.New("WebRTC session already has a connection")
	// ErrConnectionSessionMismatch prevents one session from mutating another session's connection.
	ErrConnectionSessionMismatch = errors.New("WebRTC connection session mismatch")
	// ErrConnectionStateInvalid rejects transport states outside the shared six-state contract.
	ErrConnectionStateInvalid = errors.New("invalid WebRTC connection state")
	// ErrConnectionStateTransition rejects callbacks that violate the transport state machine.
	ErrConnectionStateTransition = errors.New("invalid WebRTC connection state transition")
	// ErrConnectionStateTimeRequired rejects state callbacks without an observation timestamp.
	ErrConnectionStateTimeRequired = errors.New("WebRTC connection state time is required")
	// ErrConnectionStateStale rejects callbacks that are not newer than the retained snapshot.
	ErrConnectionStateStale = errors.New("stale WebRTC connection state callback")
	// ErrTicketSessionMismatch prevents a ticket for one session from authorizing another.
	ErrTicketSessionMismatch = errors.New("realtime ticket session mismatch")
	// ErrTicketAccountRequired prevents unowned tickets from authorizing realtime media access.
	ErrTicketAccountRequired = errors.New("realtime ticket account is required")
	// ErrTicketExpired prevents a stale realtime ticket from opening a connection.
	ErrTicketExpired = errors.New("realtime ticket expired")
	// ErrRealtimeTokenRequired prevents unauthenticated signaling commands from reaching the ticket validator.
	ErrRealtimeTokenRequired = errors.New("realtime ticket token is required")
)
