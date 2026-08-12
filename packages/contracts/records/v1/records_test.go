package recordsv1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type finalTurnSinkStub struct{}

func (finalTurnSinkStub) Publish(context.Context, FinalTurnEvent) error { return nil }

type finalTurnConsumerStub struct{}

func (finalTurnConsumerStub) ConsumeFinalTurn(context.Context, FinalTurnEvent) error { return nil }

type turnReaderStub struct{}

func (turnReaderStub) ReadFinalTurns(context.Context, string, []string) ([]FinalTurnSnapshot, error) {
	return nil, nil
}

type sessionOwnerReaderStub struct{}

func (sessionOwnerReaderStub) AccountIDForSession(context.Context, string) (string, error) {
	return "", nil
}

var (
	_ FinalTurnSink      = finalTurnSinkStub{}
	_ FinalTurnConsumer  = finalTurnConsumerStub{}
	_ TurnReader         = turnReaderStub{}
	_ SessionOwnerReader = sessionOwnerReaderStub{}
)

func TestFinalTurnEventJSONPreservesNullableAttribution(t *testing.T) {
	event := validFinalTurnEvent()

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal final turn event: %v", err)
	}

	var actual map[string]any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("unmarshal final turn event: %v", err)
	}

	if actual["participant_id"] != nil {
		t.Fatalf("participant_id = %v, want null", actual["participant_id"])
	}
	if actual["speaker_label_snapshot"] != nil {
		t.Fatalf("speaker_label_snapshot = %v, want null", actual["speaker_label_snapshot"])
	}
	if got, want := actual["language_config_version"], float64(3); got != want {
		t.Fatalf("language_config_version = %v, want %v", got, want)
	}
	if got, want := actual["event_version"], float64(FinalTurnEventVersion); got != want {
		t.Fatalf("event_version = %v, want %v", got, want)
	}
	if got, want := actual["attribution_status"], string(AttributionPending); got != want {
		t.Fatalf("attribution_status = %v, want %q", got, want)
	}
}

func TestFinalTurnEventCarriesOptionalInternalIDs(t *testing.T) {
	event := validFinalTurnEvent()
	providerID := "diar_01"
	asrProfileID := "asr_profile_01"
	ttsProfileID := "tts_profile_01"
	event.ProviderSpeakerID = &providerID
	event.ASRProfileID = &asrProfileID
	event.TTSProfileID = &ttsProfileID

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal final turn event: %v", err)
	}

	var actual map[string]any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("unmarshal final turn event: %v", err)
	}
	if got, want := actual["provider_speaker_id"], providerID; got != want {
		t.Fatalf("provider_speaker_id = %v, want %q", got, want)
	}
	if got, want := actual["asr_profile_id"], asrProfileID; got != want {
		t.Fatalf("asr_profile_id = %v, want %q", got, want)
	}
	if got, want := actual["tts_profile_id"], ttsProfileID; got != want {
		t.Fatalf("tts_profile_id = %v, want %q", got, want)
	}

	event.ProviderSpeakerID = nil
	event.ASRProfileID = nil
	event.TTSProfileID = nil
	body, err = json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal final turn event without provider id: %v", err)
	}
	var withoutProvider map[string]any
	if err := json.Unmarshal(body, &withoutProvider); err != nil {
		t.Fatalf("unmarshal final turn event without provider id: %v", err)
	}
	if _, present := withoutProvider["provider_speaker_id"]; present {
		t.Fatalf("provider_speaker_id should be omitted when nil")
	}
	for _, field := range []string{"asr_profile_id", "tts_profile_id"} {
		if _, present := withoutProvider[field]; present {
			t.Fatalf("%s should be omitted when nil", field)
		}
	}
}

func TestFinalTurnEventValidatesRequiredFields(t *testing.T) {
	valid := validFinalTurnEvent()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid FinalTurnEvent error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*FinalTurnEvent)
	}{
		{name: "event version", mutate: func(event *FinalTurnEvent) { event.EventVersion = 2 }},
		{name: "event id", mutate: func(event *FinalTurnEvent) { event.EventID = "" }},
		{name: "trace id", mutate: func(event *FinalTurnEvent) { event.TraceID = "" }},
		{name: "turn id", mutate: func(event *FinalTurnEvent) { event.TurnID = "" }},
		{name: "session id", mutate: func(event *FinalTurnEvent) { event.SessionID = "" }},
		{name: "sequence number", mutate: func(event *FinalTurnEvent) { event.SequenceNo = 0 }},
		{name: "source language", mutate: func(event *FinalTurnEvent) { event.SourceLanguage = "" }},
		{name: "target language", mutate: func(event *FinalTurnEvent) { event.TargetLanguage = "" }},
		{name: "source text", mutate: func(event *FinalTurnEvent) { event.SourceText = "" }},
		{name: "translated text", mutate: func(event *FinalTurnEvent) { event.TranslatedText = "" }},
		{name: "speaker code", mutate: func(event *FinalTurnEvent) { event.SpeakerCode = "" }},
		{name: "language config version", mutate: func(event *FinalTurnEvent) { event.LanguageConfigVersion = 0 }},
		{name: "blank ASR profile ID", mutate: func(event *FinalTurnEvent) { value := " \t"; event.ASRProfileID = &value }},
		{name: "blank TTS profile ID", mutate: func(event *FinalTurnEvent) { value := " \t"; event.TTSProfileID = &value }},
		{name: "attribution status", mutate: func(event *FinalTurnEvent) { event.AttributionStatus = "unknown" }},
		{name: "started at", mutate: func(event *FinalTurnEvent) { event.StartedAt = time.Time{} }},
		{name: "ended before start", mutate: func(event *FinalTurnEvent) { event.EndedAt = event.StartedAt.Add(-time.Second) }},
		{name: "occurred at", mutate: func(event *FinalTurnEvent) { event.OccurredAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := validFinalTurnEvent()
			test.mutate(&event)
			if err := event.Validate(); !errors.Is(err, ErrInvalidFinalTurnEvent) {
				t.Fatalf("Validate() error = %v, want ErrInvalidFinalTurnEvent", err)
			}
		})
	}
}

func TestFinalTurnEventPayloadHashCoversCompleteEvent(t *testing.T) {
	event := validFinalTurnEvent()
	hash, err := FinalTurnEventPayloadHash(event)
	if err != nil {
		t.Fatalf("FinalTurnEventPayloadHash() error = %v", err)
	}
	replayHash, err := FinalTurnEventPayloadHash(event)
	if err != nil {
		t.Fatalf("FinalTurnEventPayloadHash() replay error = %v", err)
	}
	if hash != replayHash {
		t.Fatalf("replay hash = %x, want %x", replayHash, hash)
	}

	for _, mutate := range []func(*FinalTurnEvent){
		func(event *FinalTurnEvent) { event.TraceID = "trace_02" },
		func(event *FinalTurnEvent) { event.OccurredAt = event.OccurredAt.Add(time.Second) },
		func(event *FinalTurnEvent) { event.TranslatedText = "different translation" },
		func(event *FinalTurnEvent) { value := "asr_profile_02"; event.ASRProfileID = &value },
		func(event *FinalTurnEvent) { value := "tts_profile_02"; event.TTSProfileID = &value },
	} {
		changed := event
		mutate(&changed)
		changedHash, err := FinalTurnEventPayloadHash(changed)
		if err != nil {
			t.Fatalf("FinalTurnEventPayloadHash() changed event error = %v", err)
		}
		if changedHash == hash {
			t.Fatalf("changed event hash = %x, want a different hash", changedHash)
		}
	}
}

func TestFinalTurnPayloadHashRejectsLegacyReplayWithProfileIDs(t *testing.T) {
	legacy := validFinalTurnEvent()
	legacyHash, err := FinalTurnEventPayloadHash(legacy)
	if err != nil {
		t.Fatalf("FinalTurnEventPayloadHash() legacy error = %v", err)
	}

	current := legacy
	asrProfileID := "asr_profile_01"
	ttsProfileID := "tts_profile_01"
	current.ASRProfileID = &asrProfileID
	current.TTSProfileID = &ttsProfileID
	matched, err := FinalTurnEventPayloadHashMatches(current, legacyHash[:])
	if err != nil {
		t.Fatalf("FinalTurnEventPayloadHashMatches() error = %v", err)
	}
	if matched {
		t.Fatal("legacy payload hash unexpectedly accepted profile-added replay")
	}
}

func TestFinalTurnPayloadHashMatchesLegacyPayloadWithoutProviderSpeakerID(t *testing.T) {
	legacyJSON := `{"event_version":1,"event_id":"evt_01","trace_id":"trace_01","turn_id":"vt_01","session_id":"vs_01","participant_id":null,"sequence_no":1,"source_language":"zh-CN","target_language":"en-US","language_config_version":3,"source_text":"hello","translated_text":"hello","speaker_code":"speaker_01","speaker_label_snapshot":null,"speaker_confidence":null,"attribution_status":"pending","started_at":"2026-07-24T08:00:00Z","ended_at":"2026-07-24T08:00:01Z","occurred_at":"2026-07-24T08:00:02Z"}`

	legacyHash := sha256.Sum256([]byte(legacyJSON))

	current := validFinalTurnEvent()
	current.ProviderSpeakerID = nil
	currentHash, err := FinalTurnEventPayloadHash(current)
	if err != nil {
		t.Fatalf("FinalTurnEventPayloadHash() error = %v", err)
	}
	if currentHash != legacyHash {
		t.Fatalf("current hash = %x, want legacy %x", currentHash, legacyHash)
	}

	var decoded FinalTurnEvent
	if err := json.Unmarshal([]byte(legacyJSON), &decoded); err != nil {
		t.Fatalf("unmarshal legacy payload: %v", err)
	}
	decodedHash, err := FinalTurnEventPayloadHash(decoded)
	if err != nil {
		t.Fatalf("FinalTurnEventPayloadHash() legacy decode error = %v", err)
	}
	if decodedHash != legacyHash {
		t.Fatalf("decoded legacy hash = %x, want %x", decodedHash, legacyHash)
	}
}

func TestFinalTurnEventPayloadHashMatchesLegacyRoutePayload(t *testing.T) {
	event := validFinalTurnEvent()
	event.TTSEnabled = true
	event.DeliveryEnabled = true
	legacyEvent := event
	legacyEvent.TTSEnabled = false
	legacyEvent.DeliveryEnabled = false
	legacyHash, err := FinalTurnEventPayloadHash(legacyEvent)
	if err != nil {
		t.Fatalf("legacy hash error = %v", err)
	}

	matched, err := FinalTurnEventPayloadHashMatches(event, legacyHash[:])
	if err != nil {
		t.Fatalf("hash compatibility check error = %v", err)
	}
	if !matched {
		t.Fatal("legacy route payload hash was not accepted")
	}

	changed := event
	changed.TranslatedText = "different translation"
	matched, err = FinalTurnEventPayloadHashMatches(changed, legacyHash[:])
	if err != nil {
		t.Fatalf("changed hash compatibility check error = %v", err)
	}
	if matched {
		t.Fatal("changed payload unexpectedly matched legacy hash")
	}
}

func validFinalTurnEvent() FinalTurnEvent {
	return FinalTurnEvent{
		EventVersion:          FinalTurnEventVersion,
		EventID:               "evt_01",
		TraceID:               "trace_01",
		TurnID:                "vt_01",
		SessionID:             "vs_01",
		SequenceNo:            1,
		SourceLanguage:        "zh-CN",
		TargetLanguage:        "en-US",
		LanguageConfigVersion: 3,
		SourceText:            "hello",
		TranslatedText:        "hello",
		SpeakerCode:           "speaker_01",
		AttributionStatus:     AttributionPending,
		StartedAt:             time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC),
		EndedAt:               time.Date(2026, 7, 24, 8, 0, 1, 0, time.UTC),
		OccurredAt:            time.Date(2026, 7, 24, 8, 0, 2, 0, time.UTC),
	}
}

func TestVoiceTurnJSONExposesPublicFieldNames(t *testing.T) {
	turn := VoiceTurn{
		ID:                    "vt_01",
		SessionID:             "vs_01",
		SpeakerCode:           "speaker_01",
		SequenceNo:            1,
		SourceLanguage:        "zh-CN",
		TargetLanguage:        "en-US",
		LanguageConfigVersion: 3,
		SourceText:            "hello",
		TranslatedText:        "hello",
		AttributionStatus:     AttributionProvisional,
		StartedAt:             time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC),
		EndedAt:               time.Date(2026, 7, 24, 8, 0, 1, 0, time.UTC),
		CreatedAt:             time.Date(2026, 7, 24, 8, 0, 2, 0, time.UTC),
	}

	body, err := json.Marshal(turn)
	if err != nil {
		t.Fatalf("marshal voice turn: %v", err)
	}

	var actual map[string]any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("unmarshal voice turn: %v", err)
	}

	for _, field := range []string{
		"participant_id",
		"source_language",
		"target_language",
		"language_config_version",
		"attribution_status",
		"corrected_by",
	} {
		if _, ok := actual[field]; !ok {
			t.Fatalf("voice turn JSON does not include %q", field)
		}
	}
	for _, field := range []string{"asr_profile_id", "tts_profile_id"} {
		if _, present := actual[field]; present {
			t.Fatalf("voice turn JSON must not include internal %q", field)
		}
	}
}

func TestListTurnsQueryJSONIncludesSessionID(t *testing.T) {
	query := ListTurnsQuery{SessionID: "vs_01"}

	body, err := json.Marshal(query)
	if err != nil {
		t.Fatalf("marshal list turns query: %v", err)
	}

	var actual map[string]any
	if err := json.Unmarshal(body, &actual); err != nil {
		t.Fatalf("unmarshal list turns query: %v", err)
	}

	if got, want := actual["session_id"], "vs_01"; got != want {
		t.Fatalf("session_id = %v, want %q", got, want)
	}
}
