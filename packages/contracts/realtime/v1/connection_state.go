package realtimev1

import "time"

// ConnectionState is the shared WebRTC transport state, independent of signaling and pipeline state.
type ConnectionState string

const (
	ConnectionNew          ConnectionState = "new"
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionDisconnected ConnectionState = "disconnected"
	ConnectionFailed       ConnectionState = "failed"
	ConnectionClosed       ConnectionState = "closed"
)

// Valid reports whether the state is part of the public six-state contract.
func (s ConnectionState) Valid() bool {
	switch s {
	case ConnectionNew, ConnectionConnecting, ConnectionConnected,
		ConnectionDisconnected, ConnectionFailed, ConnectionClosed:
		return true
	default:
		return false
	}
}

// Ready reports whether realtime media processing may start for this connection.
func (s ConnectionState) Ready() bool {
	return s == ConnectionConnected
}

// ConnectionSnapshot identifies the current WebRTC connection and its monotonic state version.
type ConnectionSnapshot struct {
	SessionID    string          `json:"session_id"`
	ConnectionID string          `json:"connection_id"`
	State        ConnectionState `json:"state"`
	Version      int64           `json:"version"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// ErrorCode is a stable cross-service WebRTC failure identifier.
type ErrorCode string

const (
	ErrorConnectionNotFound ErrorCode = "webrtc_connection_not_found"
	ErrorConnectionNotReady ErrorCode = "webrtc_connection_not_ready"
	ErrorConnectionFailed   ErrorCode = "webrtc_connection_failed"
)
