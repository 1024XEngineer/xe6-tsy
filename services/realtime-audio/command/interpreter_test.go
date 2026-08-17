package command

import (
	"context"
	"errors"
	"testing"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestLegacyInterpreterProducesBoundedActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		text       string
		wantAction Action
		wantMode   realtimev1.Mode
	}{
		{name: "activate interpretation", text: " 开始 同声传译。", wantAction: ActionActivateMode, wantMode: realtimev1.ModeInterpretation},
		{name: "return to assistant", text: "停止翻译", wantAction: ActionReturnToAssistant, wantMode: realtimev1.ModeAssistant},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command, err := (LegacyInterpreter{}).Interpret(t.Context(), validInterpretRequest(test.text))
			if err != nil {
				t.Fatalf("Interpret() error = %v", err)
			}
			if command.Action != test.wantAction || command.TargetMode != test.wantMode || !command.Action.Valid() {
				t.Fatalf("Interpret() = %#v, want action %q mode %q", command, test.wantAction, test.wantMode)
			}
		})
	}
}

func TestLegacyInterpreterRejectsInvalidRequestsAndNearMatches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		request InterpretRequest
		wantErr error
	}{
		{name: "missing session", request: InterpretRequest{CommandID: "command-1", Text: "停止翻译"}, wantErr: ErrInterpretRequestInvalid},
		{name: "missing command", request: InterpretRequest{SessionID: "session-1", Text: "停止翻译"}, wantErr: ErrInterpretRequestInvalid},
		{name: "empty text", request: validInterpretRequest("  "), wantErr: ErrInterpretRequestInvalid},
		{name: "near match", request: validInterpretRequest("停止同声传译"), wantErr: ErrCommandNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := (LegacyInterpreter{}).Interpret(t.Context(), test.request); !errors.Is(err, test.wantErr) {
				t.Fatalf("Interpret() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestLegacyInterpreterHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (LegacyInterpreter{}).Interpret(ctx, validInterpretRequest("停止翻译")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Interpret() error = %v, want context cancellation", err)
	}
}

func validInterpretRequest(text string) InterpretRequest {
	return InterpretRequest{SessionID: "session-1", CommandID: "command-1", Text: text, Language: "zh-CN"}
}
