package delivery

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIsSupportedChannelMatchesCurrentSchema(t *testing.T) {
	if !IsSupportedChannel(ChannelEmail) {
		t.Fatal("email channel should be supported")
	}
	if !IsSupportedChannel(ChannelWeChat) {
		t.Fatal("wechat channel should be supported")
	}
	if IsSupportedChannel(Channel("wecom_bot")) {
		t.Fatal("wecom_bot must remain unsupported until its schema migration")
	}
}

func TestDeliveryAttemptOutboxContainsRequiredEnvelope(t *testing.T) {
	attempt := DeliveryAttempt{ID: "attempt-1", MessageID: "message-1"}
	topic, eventKey, payload, err := buildDeliveryAttemptOutbox(attempt, "create-1")
	if err != nil {
		t.Fatalf("buildDeliveryAttemptOutbox() error = %v", err)
	}
	if topic != deliveryAttemptTopic {
		t.Fatalf("topic = %q, want %q", topic, deliveryAttemptTopic)
	}
	if eventKey != attempt.ID {
		t.Fatalf("event key = %q, want %q", eventKey, attempt.ID)
	}
	var envelope map[string]string
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("payload is not JSON object: %v", err)
	}
	for field, want := range map[string]string{
		"attempt_id":      attempt.ID,
		"message_id":      attempt.MessageID,
		"idempotency_key": "create-1",
	} {
		if envelope[field] != want {
			t.Errorf("payload[%q] = %q, want %q", field, envelope[field], want)
		}
	}
}

func TestFinalTurnSnapshotMatchesTurnsReadBoundary(t *testing.T) {
	snapshot := FinalTurnSnapshot{
		TurnID:                "turn-1",
		SessionID:             "session-1",
		SourceLanguage:        "zh-CN",
		TargetLanguage:        "en-US",
		LanguageConfigVersion: 1,
		SourceText:            "source",
		TranslatedText:        "translation",
		CreatedAt:             time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC),
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	encoded := string(payload)
	for _, field := range []string{
		`"turn_id"`, `"session_id"`, `"participant_id":null`,
		`"speaker_label_snapshot":null`, `"language_config_version":1`,
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
