package session

import (
	"errors"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

var (
	// ErrRuntimeNotFound reports that no runtime snapshot exists for a session.
	ErrRuntimeNotFound = errors.New("runtime snapshot not found")
	// ErrSessionNotCreated prevents realtime startup after business activation.
	ErrSessionNotCreated = errors.New("session must be created before realtime starts")
	// ErrInvalidDependency reports an incomplete lifecycle service configuration.
	ErrInvalidDependency = errors.New("invalid lifecycle dependency")
	// ErrSessionIDRequired prevents repository entries without an ownership key.
	ErrSessionIDRequired = errors.New("session id is required")
	// ErrStartOperationIDRequired prevents creating an unowned runtime.
	ErrStartOperationIDRequired = errors.New("start operation id is required")
	// ErrRuntimeCleanupRequired prevents startup while a previous stop remains incomplete.
	ErrRuntimeCleanupRequired = errors.New("runtime cleanup must complete before restart")
	// ErrInvalidRuntimeUpdate rejects incomplete processing-state fields.
	ErrInvalidRuntimeUpdate = errors.New("invalid runtime state update")
	// ErrInvalidRuntimeTransition rejects progress that conflicts with the lifecycle state machine.
	ErrInvalidRuntimeTransition = errors.New("invalid runtime state transition")
	// ErrRuntimeIdentityConflict rejects stale Turn or playback updates.
	ErrRuntimeIdentityConflict = errors.New("runtime identity conflict")
	// ErrRuntimeOperationConflict rejects takeover by another Start operation.
	ErrRuntimeOperationConflict = errors.New("runtime is owned by another start operation")
)

const (
	ErrorCodeStartFailed = string(realtimev1.RuntimeErrorStartFailed)
	ErrorCodeStopFailed  = string(realtimev1.RuntimeErrorStopFailed)
)
