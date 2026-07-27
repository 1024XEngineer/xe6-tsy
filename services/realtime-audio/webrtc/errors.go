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
	// ErrCandidateIDRequired prevents a candidate from bypassing idempotent delivery.
	ErrCandidateIDRequired = errors.New("candidate id is required")
	// ErrCandidateRequired rejects a candidate record without its SDP candidate value.
	ErrCandidateRequired = errors.New("candidate is required")
	// ErrConnectionNotFound reports a missing or already closed session connection.
	ErrConnectionNotFound = errors.New("WebRTC connection not found")
	// ErrConnectionClosing rejects offers while the session's current transport generation is closing.
	ErrConnectionClosing = errors.New("WebRTC session connections are closing")
	// ErrConnectionSessionMismatch prevents one session from mutating another session's connection.
	ErrConnectionSessionMismatch = errors.New("WebRTC connection session mismatch")
	// ErrTicketSessionMismatch prevents a ticket for one session from authorizing another.
	ErrTicketSessionMismatch = errors.New("realtime ticket session mismatch")
	// ErrTicketAccountRequired prevents unowned tickets from authorizing realtime media access.
	ErrTicketAccountRequired = errors.New("realtime ticket account is required")
	// ErrTicketExpired prevents a stale realtime ticket from opening a connection.
	ErrTicketExpired = errors.New("realtime ticket expired")
	// ErrRealtimeTokenRequired prevents unauthenticated signaling commands from reaching the ticket validator.
	ErrRealtimeTokenRequired = errors.New("realtime ticket token is required")
)
