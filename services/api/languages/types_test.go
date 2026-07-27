package languages

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLanguageConfigMarshalsNilEffectiveUntilAsNull(t *testing.T) {
	cfg := LanguageConfig{
		ID:             "lc_01TEST",
		SessionID:      "vs_01TEST",
		Version:        1,
		LanguagePairs:  []LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		Status:         StatusActive,
		EffectiveFrom:  time.Date(2026, 7, 23, 6, 5, 0, 0, time.UTC),
		EffectiveUntil: nil,
		CreatedBy:      "user_abc",
		CreatedAt:      time.Date(2026, 7, 23, 6, 5, 0, 0, time.UTC),
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	const want = `"effective_until":null`
	if !strings.Contains(string(raw), want) {
		t.Fatalf("JSON missing %s; got %s", want, raw)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal map: %v", err)
	}
	if _, ok := decoded["effective_until"]; !ok {
		t.Fatal("effective_until key omitted from JSON object")
	}
	if string(decoded["effective_until"]) != "null" {
		t.Fatalf("effective_until = %s, want null", decoded["effective_until"])
	}
}
