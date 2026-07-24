package session

import "errors"

var (
	// ErrRuntimeNotFound reports that no runtime snapshot exists for a session.
	ErrRuntimeNotFound = errors.New("runtime snapshot not found")
	// ErrSessionNotCreated prevents realtime startup after business activation.
	ErrSessionNotCreated = errors.New("session must be created before realtime starts")
	// ErrInvalidDependency reports an incomplete lifecycle service configuration.
	ErrInvalidDependency = errors.New("invalid lifecycle dependency")
	// ErrSessionIDRequired prevents repository entries without an ownership key.
	ErrSessionIDRequired = errors.New("session id is required")
)

const (
	ErrorCodeStartFailed = "realtime_start_failed"
	ErrorCodeStopFailed  = "realtime_stop_failed"
)
