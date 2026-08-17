package command

import (
	"context"
	"errors"
	"strings"
	"unicode"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

var (
	ErrCommandNotAllowed       = errors.New("voice command is not in the allowlist")
	ErrInterpretRequestInvalid = errors.New("command interpretation request is invalid")
)

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

var commandAllowlist = map[string]Candidate{
	"开始同声传译": {
		Text: "开始同声传译", Action: ActionActivateMode, TargetMode: realtimev1.ModeInterpretation,
	},
	"停止翻译": {
		Text: "停止翻译", Action: ActionReturnToAssistant, TargetMode: realtimev1.ModeAssistant,
	},
}

// LegacyInterpreter preserves the two deterministic commands while the semantic provider and
// capability registry are introduced incrementally. It must be explicitly injected and is not a
// fallback for failed semantic interpretation.
type LegacyInterpreter struct{}

// Interpret returns a compatibility command without producing any side effects.
func (LegacyInterpreter) Interpret(ctx context.Context, request InterpretRequest) (Candidate, error) {
	if err := ctx.Err(); err != nil {
		return Candidate{}, err
	}
	if request.SessionID == "" || request.CommandID == "" || strings.TrimSpace(request.Text) == "" {
		return Candidate{}, ErrInterpretRequestInvalid
	}
	return parseLegacyCommand(request.Text)
}

// Parse accepts only the two product-approved commands. Normalization removes whitespace and
// sentence-ending punctuation that an ASR provider may add; it never uses fuzzy or semantic
// matching, so ordinary speech cannot expand the executable command surface.
func Parse(text string) (Command, error) {
	candidate, err := parseLegacyCommand(text)
	if err != nil {
		return Command{}, err
	}
	return Command{
		Text: candidate.Text, Action: candidate.Action, TargetMode: candidate.TargetMode,
		Arguments: candidate.Arguments,
	}, nil
}

func parseLegacyCommand(text string) (Candidate, error) {
	normalized := normalize(text)
	parsed, ok := commandAllowlist[normalized]
	if !ok {
		return Candidate{}, ErrCommandNotAllowed
	}
	return parsed, nil
}

var _ Interpreter = LegacyInterpreter{}

func normalize(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	return strings.TrimRight(text, "，。！？,.!?")
}
