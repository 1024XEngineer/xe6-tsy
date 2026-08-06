package delivery

import (
	"strings"
	"testing"
)

func TestFormatEmailDeliveryBodyIncludesTurnText(t *testing.T) {
	body := formatEmailDeliveryBody(SendRequest{
		Message: Message{
			Turns: []FinalTurnSnapshot{{
				TurnID:         "turn-1",
				SourceText:     "hello",
				TranslatedText: "你好",
			}},
		},
	})
	if body == "" {
		t.Fatal("formatEmailDeliveryBody() returned empty body")
	}
	for _, want := range []string{"turn-1", "hello", "你好"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %q, want substring %q", body, want)
		}
	}
}

func TestNewSMTPProviderRequiresMailer(t *testing.T) {
	if _, err := NewSMTPProvider(nil); err == nil {
		t.Fatal("NewSMTPProvider(nil) error = nil, want error")
	}
}
