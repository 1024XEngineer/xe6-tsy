package webrtc

import (
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// ConnectionTicket is the short-lived session authorization issued by the API service.
type ConnectionTicket struct {
	SessionID string
	AccountID string
	ExpiresAt time.Time
}

// SessionDescription holds the SDP body and its WebRTC type.
type SessionDescription struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

// OfferRequest is the authenticated signaling command for one client offer.
type OfferRequest struct {
	SDP            string `json:"sdp"`
	Type           string `json:"type"`
	IdempotencyKey string `json:"-"`
}

// OfferResponse is the transport-neutral result returned after a connection is reserved.
type OfferResponse struct {
	SDP              string                     `json:"sdp"`
	Type             string                     `json:"type"`
	SessionID        string                     `json:"session_id"`
	ConnectionID     string                     `json:"connection_id"`
	DataChannelLabel string                     `json:"data_channel_label"`
	TTSTrackID       string                     `json:"tts_track_id"`
	ConnectionState  realtimev1.ConnectionState `json:"connection_state"`
}

// ICECandidate is one idempotent candidate supplied after an offer.
type ICECandidate struct {
	ID               string  `json:"candidate_id"`
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdp_mid"`
	SDPMLineIndex    *uint16 `json:"sdp_mline_index"`
	UsernameFragment *string `json:"username_fragment"`
}

// CandidateRequest batches candidates belonging to one connection.
type CandidateRequest struct {
	ConnectionID    string         `json:"connection_id"`
	Candidates      []ICECandidate `json:"candidates"`
	EndOfCandidates bool           `json:"end_of_candidates"`
}

// CandidateResponse distinguishes new candidates from idempotent repeats.
type CandidateResponse struct {
	ConnectionID             string   `json:"connection_id"`
	AcceptedCandidateIDs     []string `json:"accepted_candidate_ids"`
	DeduplicatedCandidateIDs []string `json:"deduplicated_candidate_ids"`
	EndOfCandidates          bool     `json:"end_of_candidates"`
}

// OpenConnectionRequest is the manager command after ticket validation and before transport creation.
type OpenConnectionRequest struct {
	SessionID      string
	IdempotencyKey string
	Offer          SessionDescription
	CreatedAt      time.Time
}

// Connection is the runtime metadata retained by the signaling skeleton.
// SDP values are retained only by the in-memory skeleton and are not a persistence contract.
type Connection struct {
	ID             string
	SessionID      string
	IdempotencyKey string
	Offer          SessionDescription
	Answer         SessionDescription
	State          realtimev1.ConnectionState
	CreatedAt      time.Time
}
