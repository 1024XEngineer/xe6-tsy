package controlplane

import (
	"context"
	"net/http"
	"strings"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// ModeControl exposes only the runtime-owned business mode state and its
// compare-and-switch operation. Implementations must not rebuild media or
// WebRTC resources while applying a command.
type ModeControl interface {
	GetModeState(context.Context, string) (realtimev1.ModeStateSnapshot, error)
	SwitchMode(context.Context, realtimev1.SwitchModeCommand) (realtimev1.SwitchModeResult, error)
}

func (h *Handler) modeState(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("session_id")
	if _, err := h.authorize(request.Context(), request, sessionID); err != nil {
		h.writeError(writer, request, err)
		return
	}
	state, err := h.modes.GetModeState(request.Context(), sessionID)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	h.writeJSON(writer, http.StatusOK, state)
}

func (h *Handler) switchMode(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("session_id")
	if _, err := h.authorize(request.Context(), request, sessionID); err != nil {
		h.writeError(writer, request, err)
		return
	}
	idempotencyKey, err := requiredIdempotencyKey(request)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	var command realtimev1.SwitchModeCommand
	if err := decodeJSON(request, &command, false); err != nil {
		h.writeError(writer, request, err)
		return
	}
	if !validSwitchModeCommand(sessionID, command) {
		h.writeError(writer, request, ErrInvalidRequest)
		return
	}

	replayKey := "mode\x00" + sessionID + "\x00" + idempotencyKey
	h.handleReplay(writer, request.Context(), sessionID, replayKey, command, func() (any, error) {
		return h.modes.SwitchMode(request.Context(), command)
	})
}

func validSwitchModeCommand(sessionID string, command realtimev1.SwitchModeCommand) bool {
	return strings.TrimSpace(sessionID) != "" && command.SessionID == sessionID &&
		strings.TrimSpace(command.RuntimeInstanceID) != "" && strings.TrimSpace(command.OperationID) != "" &&
		strings.TrimSpace(command.TraceID) != "" && command.ExpectedGeneration >= 1 && command.TargetMode.Valid()
}
