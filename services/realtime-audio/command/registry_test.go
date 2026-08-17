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
