package command

import (
	"errors"
	"fmt"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

var (
	ErrCapabilityInvalid     = errors.New("command capability descriptor is invalid")
	ErrCapabilityDuplicate   = errors.New("command capability is already registered")
	ErrCapabilityUnavailable = errors.New("command capability is unavailable")
	ErrCandidateInvalid      = errors.New("command candidate is invalid")
)

// Validator is the deterministic trust boundary between provider output and execution.
type Validator interface {
	Validate(Candidate) (Command, error)
}

// ValidatorFunc adapts a function to the deterministic validation boundary.
type ValidatorFunc func(Candidate) (Command, error)

func (f ValidatorFunc) Validate(candidate Candidate) (Command, error) {
	return f(candidate)
}

// CapabilityDescriptor is the executable surface exposed to interpreters and validators. The
// registry must be assembled from modes that have real handlers; descriptions are prompt data,
// while Actions and SchemaVersion are the deterministic authority.
type CapabilityDescriptor struct {
	Mode          realtimev1.Mode
	Description   string
	SchemaVersion int
	Actions       []Action
}

// Registry is an immutable capability lookup assembled during process wiring.
type Registry struct {
	ordered []CapabilityDescriptor
	byMode  map[realtimev1.Mode]CapabilityDescriptor
}

// NewRegistry rejects partial or duplicate descriptors so prompt-visible capabilities and the
// deterministic execution surface cannot silently diverge.
func NewRegistry(descriptors ...CapabilityDescriptor) (*Registry, error) {
	registry := &Registry{byMode: make(map[realtimev1.Mode]CapabilityDescriptor, len(descriptors))}
	for _, descriptor := range descriptors {
		if err := validateDescriptor(descriptor); err != nil {
			return nil, err
		}
		if _, exists := registry.byMode[descriptor.Mode]; exists {
			return nil, fmt.Errorf("%w: %s", ErrCapabilityDuplicate, descriptor.Mode)
		}
		descriptor.Actions = append([]Action(nil), descriptor.Actions...)
		registry.byMode[descriptor.Mode] = descriptor
		registry.ordered = append(registry.ordered, descriptor)
	}
	return registry, nil
}

// Descriptors returns a defensive snapshot suitable for prompt construction.
func (r *Registry) Descriptors() []CapabilityDescriptor {
	if r == nil {
		return nil
	}
	result := make([]CapabilityDescriptor, 0, len(r.ordered))
	for _, descriptor := range r.ordered {
		descriptor.Actions = append([]Action(nil), descriptor.Actions...)
		result = append(result, descriptor)
	}
	return result
}

// Validate converts an untrusted candidate into a bounded executable command. Capability and
// action checks happen again even when the provider claims to have used the supplied prompt.
func (r *Registry) Validate(candidate Candidate) (Command, error) {
	if r == nil || !candidate.Action.Valid() || !candidate.TargetMode.Valid() {
		return Command{}, ErrCandidateInvalid
	}
	descriptor, ok := r.byMode[candidate.TargetMode]
	if !ok {
		return Command{}, fmt.Errorf("%w: %s", ErrCapabilityUnavailable, candidate.TargetMode)
	}
	if !containsAction(descriptor.Actions, candidate.Action) {
		return Command{}, fmt.Errorf("%w: action %s for %s", ErrCandidateInvalid, candidate.Action, candidate.TargetMode)
	}
	if candidate.Action == ActionReturnToAssistant && candidate.TargetMode != realtimev1.ModeAssistant {
		return Command{}, fmt.Errorf("%w: return target", ErrCandidateInvalid)
	}
	if candidate.Action == ActionAssistantQuery && candidate.TargetMode != realtimev1.ModeAssistant {
		return Command{}, fmt.Errorf("%w: assistant query target", ErrCandidateInvalid)
	}
	if candidate.TargetMode != realtimev1.ModeInterpretation && candidate.Arguments != (Arguments{}) {
		return Command{}, fmt.Errorf("%w: arguments for %s", ErrCandidateInvalid, candidate.TargetMode)
	}
	return Command{
		Text: candidate.Text, Action: candidate.Action, TargetMode: candidate.TargetMode,
		Arguments: candidate.Arguments,
	}, nil
}

func validateDescriptor(descriptor CapabilityDescriptor) error {
	if !descriptor.Mode.Valid() || descriptor.Description == "" || descriptor.SchemaVersion <= 0 || len(descriptor.Actions) == 0 {
		return fmt.Errorf("%w: %s", ErrCapabilityInvalid, descriptor.Mode)
	}
	seen := make(map[Action]struct{}, len(descriptor.Actions))
	for _, action := range descriptor.Actions {
		if !action.Valid() {
			return fmt.Errorf("%w: action %s", ErrCapabilityInvalid, action)
		}
		if _, duplicate := seen[action]; duplicate {
			return fmt.Errorf("%w: duplicate action %s", ErrCapabilityInvalid, action)
		}
		seen[action] = struct{}{}
	}
	return nil
}

func containsAction(actions []Action, target Action) bool {
	for _, action := range actions {
		if action == target {
			return true
		}
	}
	return false
}

var _ Validator = (*Registry)(nil)
