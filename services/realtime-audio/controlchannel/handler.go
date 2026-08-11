// Package controlchannel adapts authenticated WebRTC control messages to the
// runtime-owned mode coordinator without depending on Pion or HTTP.
package controlchannel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

var (
	ErrModeControlRequired          = errors.New("control channel mode control is required")
	ErrModeControlAlreadyConfigured = errors.New("control channel mode control is already configured")
)

// ModeControl is the runtime-owned mode boundary shared by HTTP and WebRTC
// control commands. Implementations must preserve runtime-scoped CAS and replay
// semantics and must not rebuild the PeerConnection during a mode switch.
type ModeControl interface {
	SwitchMode(context.Context, realtimev1.SwitchModeCommand) (realtimev1.SwitchModeResult, error)
}

// Handler promotes transport-safe commands into runtime commands. It is built
// before the runtime Manager to break the connection-factory initialization
// cycle, then receives its ModeControl exactly once before serving traffic.
type Handler struct {
	mu    sync.RWMutex
	modes ModeControl
}

// NewHandler returns an unconfigured bridge. SetModeControl must complete
// before the WebRTC signaling handler becomes reachable.
func NewHandler() *Handler {
	return &Handler{}
}

// SetModeControl completes one-time runtime wiring. Rejecting replacement keeps
// established connections from observing a different mode authority midway
// through their lifetime.
func (h *Handler) SetModeControl(modes ModeControl) error {
	if h == nil || modes == nil {
		return ErrModeControlRequired
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.modes != nil {
		return ErrModeControlAlreadyConfigured
	}
	h.modes = modes
	return nil
}

// HandleModeSwitch applies one command to the Session authorized when the
// PeerConnection was opened. Session identity is never accepted from message
// payloads; connectionID is required as proof that the transport supplied a
// concrete connection binding, although it is not part of runtime idempotency.
func (h *Handler) HandleModeSwitch(
	ctx context.Context,
	boundSessionID string,
	boundConnectionID string,
	requestID string,
	command realtimev1.ControlModeSwitchCommand,
) realtimev1.ControlResponse {
	if ctx == nil {
		return errorResponse(requestID, realtimev1.ErrorControlInvalidMessage)
	}
	if err := ctx.Err(); err != nil {
		return errorResponse(requestID, controlErrorCode(err))
	}
	if !validBinding(boundSessionID) || !validBinding(boundConnectionID) {
		return errorResponse(requestID, realtimev1.ErrorControlUnauthorizedSession)
	}
	request := realtimev1.ControlModeSwitchRequest{
		ProtocolVersion: realtimev1.ControlProtocolVersion,
		Type:            realtimev1.ControlMessageModeSwitch,
		RequestID:       requestID,
		Command:         command,
	}
	if err := request.Validate(); err != nil {
		return errorResponse(requestID, realtimev1.ErrorControlInvalidMessage)
	}

	modes := h.modeControl()
	if modes == nil {
		return errorResponse(requestID, realtimev1.ErrorControlUnavailable)
	}
	result, err := modes.SwitchMode(ctx, realtimev1.SwitchModeCommand{
		SessionID:          boundSessionID,
		RuntimeInstanceID:  command.RuntimeInstanceID,
		OperationID:        command.OperationID,
		TraceID:            controlTraceID(boundSessionID, command.OperationID),
		ExpectedGeneration: command.ExpectedGeneration,
		TargetMode:         command.TargetMode,
	})
	if err != nil {
		return errorResponse(requestID, controlErrorCode(err))
	}
	response := realtimev1.ControlResponse{
		ProtocolVersion: realtimev1.ControlProtocolVersion,
		Type:            realtimev1.ControlMessageModeSwitchResult,
		RequestID:       requestID,
		Result:          &result,
	}
	if err := response.Validate(); err != nil {
		return errorResponse(requestID, realtimev1.ErrorControlUnavailable)
	}
	return response
}

func (h *Handler) modeControl() ModeControl {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.modes
}

func validBinding(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

// controlTraceID is independent of connection generation so an uncertain
// delivery retried after reconnect reaches the coordinator as the same command.
func controlTraceID(sessionID string, operationID string) string {
	digest := sha256.Sum256([]byte(sessionID + "\x00" + operationID))
	return "control_" + hex.EncodeToString(digest[:])
}

func errorResponse(requestID string, code realtimev1.ControlPlaneErrorCode) realtimev1.ControlResponse {
	response := realtimev1.ControlResponse{
		ProtocolVersion: realtimev1.ControlProtocolVersion,
		Type:            realtimev1.ControlMessageError,
		RequestID:       requestID,
		Error: &realtimev1.ControlError{
			Code:    code,
			Message: string(code),
		},
	}
	// Malformed correlation IDs must not make the error response malformed too.
	// The contract permits an empty RequestID when input could not be decoded far
	// enough to establish a safe correlation value.
	if err := response.Validate(); err != nil {
		response.RequestID = ""
	}
	return response
}

func controlErrorCode(err error) realtimev1.ControlPlaneErrorCode {
	switch {
	case errors.Is(err, runtime.ErrModeNotAvailable):
		return realtimev1.ErrorModeNotAvailable
	case errors.Is(err, runtime.ErrModeGenerationConflict):
		return realtimev1.ErrorModeGenerationConflict
	case errors.Is(err, runtime.ErrModeRuntimeInstanceMismatch):
		return realtimev1.ErrorModeRuntimeInstanceMismatch
	case errors.Is(err, runtime.ErrModeOperationConflict):
		return realtimev1.ErrorModeOperationConflict
	case errors.Is(err, session.ErrRuntimeOperationConflict):
		return realtimev1.ErrorRuntimeOperationConflict
	case errors.Is(err, runtime.ErrModeCommandInvalid):
		return realtimev1.ErrorControlInvalidMessage
	case errors.Is(err, runtime.ErrSessionIDRequired), errors.Is(err, session.ErrSessionIDRequired):
		return realtimev1.ErrorControlUnauthorizedSession
	case errors.Is(err, context.Canceled):
		return realtimev1.ErrorControlConnectionClosed
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, session.ErrRuntimeNotFound),
		errors.Is(err, runtime.ErrModeTransitionPending),
		errors.Is(err, runtime.ErrModeEventUnavailable),
		errors.Is(err, runtime.ErrDependencyRequired):
		return realtimev1.ErrorControlUnavailable
	default:
		return realtimev1.ErrorControlUnavailable
	}
}

var _ ModeControl = (*runtime.Manager)(nil)
