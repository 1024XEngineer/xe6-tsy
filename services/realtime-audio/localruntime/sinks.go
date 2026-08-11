package localruntime

import (
	"context"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

// DiscardAudioSink accepts TTS PCM without sending it to a browser track.
type DiscardAudioSink struct{}

func (DiscardAudioSink) Publish(context.Context, pipeline.AudioChunk) error { return nil }

// MemoryUsageSink records usage facts in process memory for local demos.
type MemoryUsageSink struct {
	mu    sync.Mutex
	facts []pipeline.UsageFact
}

// DataChannelAssistantReplySink publishes finalized assistant replies over the
// same WebRTC DataChannel used by translation events.
type DataChannelAssistantReplySink struct {
	Media MediaLookup
}

type FrontendAssistantReply struct {
	Type              string    `json:"type"`
	Event             string    `json:"event"`
	EventVersion      int       `json:"event_version"`
	ID                string    `json:"id"`
	TraceID           string    `json:"trace_id"`
	SessionID         string    `json:"session_id"`
	TurnID            string    `json:"turn_id"`
	RuntimeInstanceID string    `json:"runtime_instance_id"`
	Generation        int64     `json:"generation"`
	Text              string    `json:"text"`
	Language          string    `json:"language"`
	OccurredAt        time.Time `json:"occurred_at"`
}

func (s DataChannelAssistantReplySink) Publish(ctx context.Context, event realtimev1.AssistantReplyEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Media == nil {
		return nil
	}
	media, err := s.Media.CurrentMedia(ctx, event.SessionID)
	if err != nil || media.TranslationEvents() == nil {
		return nil
	}
	payload := FrontendAssistantReply{
		Type: "assistant.reply", Event: "assistant.reply",
		EventVersion: event.EventVersion, ID: event.EventID, TraceID: event.TraceID,
		SessionID: event.SessionID, TurnID: event.TurnID,
		RuntimeInstanceID: event.RuntimeInstanceID, Generation: event.Generation,
		Text: event.Text, Language: event.Language, OccurredAt: event.OccurredAt,
	}
	publishCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = media.TranslationEvents().PublishJSON(publishCtx, payload)
	return nil
}

var _ pipeline.AssistantReplySink = DataChannelAssistantReplySink{}

func (s *MemoryUsageSink) Publish(_ context.Context, fact pipeline.UsageFact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts = append(s.facts, fact)
	return nil
}

func (s *MemoryUsageSink) Facts() []pipeline.UsageFact {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]pipeline.UsageFact(nil), s.facts...)
}

// MediaLookup resolves the current media transport for a session.
type MediaLookup interface {
	CurrentMedia(ctx context.Context, sessionID string) (webrtc.MediaTransport, error)
}

// DataChannelFinalTurnSink publishes browser-facing translation.final events.
type DataChannelFinalTurnSink struct {
	Media MediaLookup
}

// FrontendTranslationFinal matches the lingow-voice-demo DataChannel listener.
type FrontendTranslationFinal struct {
	Type            string `json:"type"`
	Event           string `json:"event"`
	TurnID          string `json:"turn_id"`
	ID              string `json:"id"`
	SessionID       string `json:"session_id"`
	SourceText      string `json:"source_text"`
	TranslatedText  string `json:"translated_text"`
	SourceLanguage  string `json:"source_language"`
	TargetLanguage  string `json:"target_language"`
	SequenceNo      int64  `json:"sequence"`
	LanguageConfigV int64  `json:"language_config_version"`
}

func (s DataChannelFinalTurnSink) Publish(ctx context.Context, event recordsv1.FinalTurnEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Subtitle delivery is best-effort for the local demo. A closed or slow
	// DataChannel must not tear down the mock ASR→LLM pipeline.
	if s.Media == nil {
		return nil
	}
	media, err := s.Media.CurrentMedia(ctx, event.SessionID)
	if err != nil {
		return nil
	}
	sink := media.TranslationEvents()
	if sink == nil {
		return nil
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
	publishCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = sink.PublishJSON(publishCtx, payload)
	return nil
}
