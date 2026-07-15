package speechport

import "context"

// ProviderRef identifies the provider and model that produced an artifact
// without exposing provider-specific payloads to the application.
type ProviderRef struct {
	Name         string `json:"name"`
	ModelVersion string `json:"model_version"`
}

// StartASRStreamRequest carries the immutable session and track references
// needed to open an ASR stream. Audio transport stays outside this boundary.
type StartASRStreamRequest struct {
	RunID          string
	SessionID      string
	TrackID        string
	Direction      string
	SourceLanguage string
}

// ASREventType separates transient partial results from final results and
// provider failures at the provider boundary.
type ASREventType string

const (
	ASREventPartial ASREventType = "partial"
	ASREventFinal   ASREventType = "final"
	ASREventError   ASREventType = "error"
)

// ASREvent is the provider-neutral event delivered by an ASR stream. Exactly
// one payload is expected for each event type.
type ASREvent struct {
	Type    ASREventType
	Partial *ASRPartial
	Final   *ASRFinal
	Err     error
}

// ASRPartial is display-only recognition output and must not become a final
// utterance or downstream business input.
type ASRPartial struct {
	SequenceNo int64
	Text       string
}

// ASRFinal preserves the source text and audio position required to trace an
// immutable transcript result back to the media track.
type ASRFinal struct {
	SequenceNo       int64
	StartMS          int64
	EndMS            int64
	AudioSourceRef   string
	SpeakerClusterID string
	SourceText       string
	Confidence       *float64
	Provider         ProviderRef
}

// ASRProvider is replaceable recognition infrastructure. Implementations must
// not own transcript state or expose provider-specific response objects.
type ASRProvider interface {
	StartStream(context.Context, StartASRStreamRequest) (ASRStream, error)
}

// ASRStream exposes provider events and an idempotent shutdown boundary. It
// does not define the application's audio upload protocol.
type ASRStream interface {
	Events() <-chan ASREvent
	Close(context.Context) error
}

// TranslateRequest keeps source and target languages explicit so source text
// is never overwritten by a translated result.
type TranslateRequest struct {
	SessionID      string
	SourceLanguage string
	TargetLanguage string
	Text           string
}

// TranslateResult is a provider-neutral translation artifact with separately
// traceable confidence and provider metadata.
type TranslateResult struct {
	Text       string
	Confidence *float64
	Provider   ProviderRef
}

// TranslationProvider is replaceable translation infrastructure and receives
// only the typed request needed for one text conversion.
type TranslationProvider interface {
	Translate(context.Context, TranslateRequest) (TranslateResult, error)
}

// SynthesizeRequest identifies a text snapshot and its playback configuration;
// synthesized audio bytes remain outside Module 4 state.
type SynthesizeRequest struct {
	SessionID      string
	SourceRef      string
	Text           string
	TextHash       string
	TargetLanguage string
	VoiceProfileID string
	AudioFormat    string
}

// SynthesizeResult returns an audio reference rather than audio bytes, keeping
// storage and playback ownership outside the speech module.
type SynthesizeResult struct {
	AudioAssetRef string
	AudioFormat   string
	DurationMS    int64
	Provider      ProviderRef
}

// TTSProvider is replaceable synthesis infrastructure. It must not mutate
// module-owned transcript or task state.
type TTSProvider interface {
	Synthesize(context.Context, SynthesizeRequest) (SynthesizeResult, error)
}
