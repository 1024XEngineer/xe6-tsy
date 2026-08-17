package command

import "errors"

var (
	// ErrClarificationRequired marks valid intent whose required arguments cannot be resolved safely.
	ErrClarificationRequired = errors.New("voice command requires clarification")
	// ErrUnsupported marks a recognized intent that is outside the registered executable surface.
	ErrUnsupported = errors.New("voice command capability or arguments are unsupported")
)
