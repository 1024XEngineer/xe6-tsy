package delivery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFinalTurnSnapshotMatchesTurnsReadBoundary(t *testing.T) {
	snapshot := FinalTurnSnapshot{
		TurnID:         "turn-1",
		SessionID:      "session-1",
		SourceLanguage: "zh-CN",
		TargetLanguage: "en-US",
		SourceText:     "source",
		TranslatedText: "translation",
		CreatedAt:      time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC),
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	encoded := string(payload)
	for _, field := range []string{
		`"turn_id"`, `"session_id"`, `"participant_id":null`,
		`"speaker_label_snapshot":null`, `"language_config_version":null`,
		`"source_text"`, `"translated_text"`, `"created_at"`,
	} {
		if !strings.Contains(encoded, field) {
			t.Errorf("payload %s does not contain %s", encoded, field)
		}
	}
}

func TestMessageAndAttemptStatusesRemainSeparate(t *testing.T) {
	message := Message{Status: MessageStatusRetrying}
	attempt := DeliveryAttempt{Status: AttemptStatusQueued}

	if message.Status != "retrying" {
		t.Fatalf("message status = %q", message.Status)
	}
	if attempt.Status != "queued" {
		t.Fatalf("attempt status = %q", attempt.Status)
	}
}
