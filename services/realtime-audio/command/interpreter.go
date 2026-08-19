package command

import (
	"context"
	"errors"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

var ErrInterpretRequestInvalid = errors.New("command interpretation request is invalid")

// Action identifies the bounded lifecycle operation requested by a spoken command. Actions are
// vendor-neutral and remain separate from modes so future capabilities can reuse the same command
// entry point without granting an interpreter direct access to runtime state.
type Action string

const (
	ActionActivateMode      Action = "activate_mode"
	ActionReturnToAssistant Action = "return_to_assistant"
	ActionAssistantQuery    Action = "assistant_query"
)

// Valid reports whether the action can currently cross the interpreter boundary.
func (a Action) Valid() bool {
	switch a {
	case ActionActivateMode, ActionReturnToAssistant, ActionAssistantQuery:
		return true
	default:
		return false
	}
}

// InterpretRequest contains one finalized command utterance and its runtime-owned identity. Text
// is untrusted ASR output; implementations must not perform mode or configuration side effects.
type InterpretRequest struct {
	SessionID string
	CommandID string
	Text      string
	Language  string
}

// Arguments is the typed, vendor-neutral argument surface shared by semantic interpreters and
// deterministic validators. Fields remain optional at the untrusted candidate boundary.
type Arguments struct {
	SourceLanguage string `json:"source_language,omitempty"`
	TargetLanguage string `json:"target_language,omitempty"`
}

// Candidate is untrusted interpreter output. It carries no callbacks or provider-specific data
// and cannot be executed until a Validator accepts its action, target capability, and arguments.
type Candidate struct {
	Text       string
	Action     Action
	TargetMode realtimev1.Mode
	Arguments  Arguments
}

// Interpreter converts natural language into an untrusted command candidate. Implementations
// must not mutate runtime state, language configuration, storage, or playback.
type Interpreter interface {
	Interpret(context.Context, InterpretRequest) (Candidate, error)
}

// InterpreterFunc adapts a function to the command interpretation boundary.
type InterpreterFunc func(context.Context, InterpretRequest) (Candidate, error)

func (f InterpreterFunc) Interpret(ctx context.Context, request InterpretRequest) (Candidate, error) {
	return f(ctx, request)
}

// Command is a deterministically validated lifecycle intent. TargetMode remains data, rather than
// an executable callback, so only the runtime coordinator can mutate active mode.
type Command struct {
	Text       string
	Action     Action
	TargetMode realtimev1.Mode
	Arguments  Arguments
}
