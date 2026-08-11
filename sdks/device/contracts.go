package device

import realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"

// Re-export the shared realtime contract types instead of defining device-only
// copies that could drift from services/realtime-audio.
type Mode = realtimev1.Mode
type ModeStateSnapshot = realtimev1.ModeStateSnapshot
type ModePhase = realtimev1.ModePhase
type ModeSwitchStatus = realtimev1.ModeSwitchStatus
type SwitchModeCommand = realtimev1.SwitchModeCommand
type SwitchModeResult = realtimev1.SwitchModeResult
type RuntimeState = realtimev1.RuntimeState
type RuntimeSnapshot = realtimev1.RuntimeSnapshot
type ConnectionState = realtimev1.ConnectionState
type ConnectionSnapshot = realtimev1.ConnectionSnapshot

const (
	ModeAssistant      = realtimev1.ModeAssistant
	ModeInterpretation = realtimev1.ModeInterpretation

	ModePhaseActive    = realtimev1.ModePhaseActive
	ModePhaseSwitching = realtimev1.ModePhaseSwitching

	ModeSwitchApplied   = realtimev1.ModeSwitchApplied
	ModeSwitchUnchanged = realtimev1.ModeSwitchUnchanged

	ConnectionNew          = realtimev1.ConnectionNew
	ConnectionConnecting   = realtimev1.ConnectionConnecting
	ConnectionConnected    = realtimev1.ConnectionConnected
	ConnectionDisconnected = realtimev1.ConnectionDisconnected
	ConnectionFailed       = realtimev1.ConnectionFailed
	ConnectionClosed       = realtimev1.ConnectionClosed

	RuntimeListening = realtimev1.RuntimeListening
)
