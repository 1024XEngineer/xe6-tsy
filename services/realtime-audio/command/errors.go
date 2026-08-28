package command

import "errors"

var (
	// ErrClarificationRequired marks valid intent whose required arguments cannot be resolved safely.
	ErrClarificationRequired = errors.New("voice command requires clarification")
	// ErrUnsupported marks a recognized intent that is outside the registered executable surface.
	ErrUnsupported = errors.New("voice command capability or arguments are unsupported")
	// ErrExecutionInterrupted marks a command whose downstream work was accepted but
	// deliberately superseded by a newer wake, mode transition, or shutdown.
	// It must not be rendered as a model or TTS failure.
	ErrExecutionInterrupted = errors.New("voice command execution interrupted")
)
