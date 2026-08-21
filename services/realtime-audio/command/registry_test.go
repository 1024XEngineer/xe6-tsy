package command

import (
	"errors"
	"testing"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestRegistryValidatesRegisteredCandidate(t *testing.T) {
	t.Parallel()
	registry := testRegistry(t)
	candidate := Candidate{
		Text: "开始同声传译，中译英", Action: ActionActivateMode,
		TargetMode: realtimev1.ModeInterpretation,
		Arguments:  Arguments{SourceLanguage: "zh-CN", TargetLanguage: "en-US"},
	}
	command, err := registry.Validate(candidate)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if command.Action != candidate.Action || command.TargetMode != candidate.TargetMode || command.Arguments != candidate.Arguments {
		t.Fatalf("Validate() = %#v, want candidate fields", command)
	}
}

func TestRegistryValidatesAssistantQueryWithoutArguments(t *testing.T) {
	t.Parallel()
	registry := testRegistry(t)
	command, err := registry.Validate(Candidate{
		Text: "帮我查一下今天上海的天气", Action: ActionAssistantQuery,
		TargetMode: realtimev1.ModeAssistant,
	})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if command.Action != ActionAssistantQuery || command.TargetMode != realtimev1.ModeAssistant {
		t.Fatalf("Validate() = %#v", command)
	}
}

func TestRegistryRejectsUntrustedCandidateSurface(t *testing.T) {
	t.Parallel()
	registry := testRegistry(t)
	tests := []struct {
		name      string
		candidate Candidate
		wantErr   error
	}{
		{name: "unknown action", candidate: Candidate{Action: "delete_session", TargetMode: realtimev1.ModeAssistant}, wantErr: ErrCandidateInvalid},
		{name: "unknown mode", candidate: Candidate{Action: ActionActivateMode, TargetMode: "english_practice"}, wantErr: ErrCandidateInvalid},
		{name: "action not registered", candidate: Candidate{Action: ActionActivateMode, TargetMode: realtimev1.ModeAssistant}, wantErr: ErrCandidateInvalid},
		{name: "return target mismatch", candidate: Candidate{Action: ActionReturnToAssistant, TargetMode: realtimev1.ModeInterpretation}, wantErr: ErrCandidateInvalid},
		{name: "assistant arguments", candidate: Candidate{Action: ActionReturnToAssistant, TargetMode: realtimev1.ModeAssistant, Arguments: Arguments{TargetLanguage: "en-US"}}, wantErr: ErrCandidateInvalid},
		{name: "assistant query target mismatch", candidate: Candidate{Action: ActionAssistantQuery, TargetMode: realtimev1.ModeInterpretation}, wantErr: ErrCandidateInvalid},
		{name: "assistant query arguments", candidate: Candidate{Action: ActionAssistantQuery, TargetMode: realtimev1.ModeAssistant, Arguments: Arguments{TargetLanguage: "en-US"}}, wantErr: ErrCandidateInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := registry.Validate(test.candidate); !errors.Is(err, test.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestRegistryRejectsInvalidDescriptorsAndReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()
	if _, err := NewRegistry(CapabilityDescriptor{}); !errors.Is(err, ErrCapabilityInvalid) {
		t.Fatalf("NewRegistry(invalid) error = %v", err)
	}
	descriptor := CapabilityDescriptor{
		Mode: realtimev1.ModeAssistant, Description: "assistant", SchemaVersion: 1,
		Actions: []Action{ActionReturnToAssistant},
	}
	if _, err := NewRegistry(descriptor, descriptor); !errors.Is(err, ErrCapabilityDuplicate) {
		t.Fatalf("NewRegistry(duplicate) error = %v", err)
	}
	registry, err := NewRegistry(descriptor)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	copy := registry.Descriptors()
	copy[0].Actions[0] = ActionActivateMode
	if registry.Descriptors()[0].Actions[0] != ActionReturnToAssistant {
		t.Fatal("Descriptors() exposed mutable registry state")
	}
}

func TestRegistryRejectsEachInvalidDescriptorField(t *testing.T) {
	valid := CapabilityDescriptor{
		Mode: realtimev1.ModeAssistant, Description: "assistant", SchemaVersion: 1,
		Actions: []Action{ActionReturnToAssistant},
	}
	tests := []struct {
		name string
		edit func(*CapabilityDescriptor)
	}{
		{name: "mode", edit: func(descriptor *CapabilityDescriptor) { descriptor.Mode = "unknown" }},
		{name: "description", edit: func(descriptor *CapabilityDescriptor) { descriptor.Description = "" }},
		{name: "zero schema", edit: func(descriptor *CapabilityDescriptor) { descriptor.SchemaVersion = 0 }},
		{name: "negative schema", edit: func(descriptor *CapabilityDescriptor) { descriptor.SchemaVersion = -1 }},
		{name: "no actions", edit: func(descriptor *CapabilityDescriptor) { descriptor.Actions = nil }},
		{name: "invalid action", edit: func(descriptor *CapabilityDescriptor) { descriptor.Actions = []Action{"unknown"} }},
		{name: "duplicate action", edit: func(descriptor *CapabilityDescriptor) {
			descriptor.Actions = []Action{ActionReturnToAssistant, ActionReturnToAssistant}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			descriptor := valid
			test.edit(&descriptor)
			if _, err := NewRegistry(descriptor); !errors.Is(err, ErrCapabilityInvalid) {
				t.Fatalf("NewRegistry() error = %v, want %v", err, ErrCapabilityInvalid)
			}
		})
	}
}

func TestRegistryValidationGuardsAndLookupBranches(t *testing.T) {
	var nilRegistry *Registry
	if descriptors := nilRegistry.Descriptors(); descriptors != nil {
		t.Fatalf("nil Descriptors() = %#v, want nil", descriptors)
	}
	if _, err := nilRegistry.Validate(Candidate{Action: ActionActivateMode, TargetMode: realtimev1.ModeInterpretation}); !errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("nil Validate() error = %v, want %v", err, ErrCandidateInvalid)
	}
	registry := testRegistry(t)
	assistantOnly, err := NewRegistry(CapabilityDescriptor{
		Mode: realtimev1.ModeAssistant, Description: "assistant", SchemaVersion: 1,
		Actions: []Action{ActionReturnToAssistant},
	})
	if err != nil {
		t.Fatalf("NewRegistry(assistantOnly) error = %v", err)
	}
	tests := []struct {
		name      string
		candidate Candidate
		want      error
		wantText  string
	}{
		{name: "invalid action", candidate: Candidate{Action: "unknown", TargetMode: realtimev1.ModeAssistant}, want: ErrCandidateInvalid, wantText: ErrCandidateInvalid.Error()},
		{name: "invalid target", candidate: Candidate{Action: ActionReturnToAssistant, TargetMode: "unknown"}, want: ErrCandidateInvalid, wantText: ErrCandidateInvalid.Error()},
		{name: "unavailable capability", candidate: Candidate{Action: ActionActivateMode, TargetMode: realtimev1.ModeInterpretation}, want: ErrCapabilityUnavailable, wantText: "command capability is unavailable: interpretation"},
		{name: "registered action second", candidate: Candidate{Action: ActionAssistantQuery, TargetMode: realtimev1.ModeAssistant}, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateRegistry := registry
			if test.name == "unavailable capability" {
				candidateRegistry = assistantOnly
			}
			_, err := candidateRegistry.Validate(test.candidate)
			if test.want == nil {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
			if test.wantText != "" && err.Error() != test.wantText {
				t.Fatalf("Validate() error text = %q, want %q", err.Error(), test.wantText)
			}
		})
	}
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	registry, err := NewRegistry(
		CapabilityDescriptor{
			Mode: realtimev1.ModeAssistant, Description: "通用助手", SchemaVersion: 1,
			Actions: []Action{ActionReturnToAssistant, ActionAssistantQuery},
		},
		CapabilityDescriptor{
			Mode: realtimev1.ModeInterpretation, Description: "双语同传", SchemaVersion: 1,
			Actions: []Action{ActionActivateMode},
		},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}
