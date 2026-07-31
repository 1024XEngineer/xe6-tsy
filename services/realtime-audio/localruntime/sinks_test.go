package localruntime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

func TestFrontendTranslationFinalJSONShape(t *testing.T) {
	event := recordsv1.FinalTurnEvent{
		EventVersion:          recordsv1.FinalTurnEventVersion,
		EventID:               "evt_1",
		TraceID:               "trace_1",
		TurnID:                "turn_1",
		SessionID:             "vs_1",
		SequenceNo:            1,
		SourceLanguage:        "zh-CN",
		TargetLanguage:        "en-US",
		SourceText:            "你好",
		TranslatedText:        "Hello",
		SpeakerCode:           recordsv1.PendingSpeakerCode,
		LanguageConfigVersion: 1,
		AttributionStatus:     recordsv1.AttributionPending,
		StartedAt:             time.Unix(1, 0).UTC(),
		EndedAt:               time.Unix(2, 0).UTC(),
		OccurredAt:            time.Unix(2, 0).UTC(),
	}
	payload := FrontendTranslationFinal{
		Type:            "translation.final",
		Event:           "translation.final",
		TurnID:          event.TurnID,
		ID:              event.EventID,
		SessionID:       event.SessionID,
		SourceText:      event.SourceText,
		TranslatedText:  event.TranslatedText,
		SourceLanguage:  event.SourceLanguage,
		TargetLanguage:  event.TargetLanguage,
		SequenceNo:      event.SequenceNo,
		LanguageConfigV: event.LanguageConfigVersion,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "translation.final" || decoded["translated_text"] != "Hello" {
		t.Fatalf("payload = %#v", decoded)
	}
}

func TestStaticLanguageConfigReaderReturnsBilingualPairs(t *testing.T) {
	snapshot, err := (StaticLanguageConfigReader{}).GetCurrentConfig(context.Background(), "vs_1")
	if err != nil {
		t.Fatalf("GetCurrentConfig() error = %v", err)
	}
	if snapshot.Status != "active" || len(snapshot.LanguagePairs) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestEnergySpeechClassifierDetectsLoudFrame(t *testing.T) {
	quiet := make([]byte, 320)
	loud := make([]byte, 320)
	for i := 0; i < len(loud); i += 2 {
		loud[i] = 0x00
		loud[i+1] = 0x40
	}
	classifier := EnergySpeechClassifier{}
	quietFrame, err := audio.NewFrame(quiet, audio.SupportedSampleRate, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	loudFrame, err := audio.NewFrame(loud, audio.SupportedSampleRate, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if classifier.Speech(quietFrame) {
		t.Fatal("quiet frame classified as speech")
	}
	if !classifier.Speech(loudFrame) {
		t.Fatal("loud frame not classified as speech")
	}
}
