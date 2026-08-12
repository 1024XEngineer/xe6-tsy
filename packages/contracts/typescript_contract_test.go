package contracts

import (
	"os"
	"strings"
	"testing"
)

func TestTypeScriptRealtimeBindingMatchesOpenAPI(t *testing.T) {
	spec := loadRootOpenAPI(t)
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	binding, err := os.ReadFile("typescript/realtime.d.ts")
	if err != nil {
		t.Fatalf("read TypeScript contract: %v", err)
	}
	source := string(binding)

	for _, schemaName := range []string{
		"RealtimeMode",
		"RealtimeRuntimeState",
		"ModePhase",
		"ModeSwitchStatus",
		"VoiceSessionStatus",
	} {
		schema := schemas[schemaName].(map[string]any)
		declaration := typescriptDeclaration(t, source, "export type "+schemaName+" =", ";")
		for _, value := range schema["enum"].([]any) {
			if !strings.Contains(declaration, `"`+value.(string)+`"`) {
				t.Fatalf("TypeScript contract missing %s value %q", schemaName, value)
			}
		}
	}

	for _, schemaName := range []string{
		"RealtimeRuntimeSnapshot",
		"ModeStateSnapshot",
		"SwitchModeCommand",
		"SwitchModeResult",
		"VoiceSessionAudioConfig",
		"VoiceSessionCapabilities",
		"VoiceSession",
	} {
		schema := schemas[schemaName].(map[string]any)
		declaration := typescriptDeclaration(t, source, "export interface "+schemaName+" {", "\n}")
		for _, field := range schema["required"].([]any) {
			if !strings.Contains(declaration, "\n  "+field.(string)+":") {
				t.Fatalf("TypeScript contract missing %s required field %q", schemaName, field)
			}
		}
	}
}

func typescriptDeclaration(t *testing.T, source, prefix, suffix string) string {
	t.Helper()
	start := strings.Index(source, prefix)
	if start < 0 {
		t.Fatalf("TypeScript contract missing declaration %q", prefix)
	}
	end := strings.Index(source[start:], suffix)
	if end < 0 {
		t.Fatalf("TypeScript contract declaration %q has no terminator", prefix)
	}
	return source[start : start+end]
}
