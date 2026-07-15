package speechport

import "context"

type ProviderRef struct {
	Name         string `json:"name"`
	ModelVersion string `json:"model_version"`
}

type StartASRStreamRequest struct {
	RunID          string
	SessionID      string
	TrackID        string
	Direction      string
	SourceLanguage string
}

type ASREventType string

const (
	ASREventPartial ASREventType = "partial"
	ASREventFinal   ASREventType = "final"
	ASREventError   ASREventType = "error"
)

type ASREvent struct {
	Type    ASREventType
	Partial *ASRPartial
	Final   *ASRFinal
	Err     error
}

type ASRPartial struct {
	SequenceNo int64
	Text       string
}

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

type ASRProvider interface {
	StartStream(context.Context, StartASRStreamRequest) (ASRStream, error)
}

type ASRStream interface {
	Events() <-chan ASREvent
	Close(context.Context) error
}

type TranslateRequest struct {
	SessionID      string
	SourceLanguage string
	TargetLanguage string
	Text           string
}

type TranslateResult struct {
	Text       string
	Confidence *float64
	Provider   ProviderRef
}

type TranslationProvider interface {
	Translate(context.Context, TranslateRequest) (TranslateResult, error)
}

type SynthesizeRequest struct {
	SessionID      string
	SourceRef      string
	Text           string
	TextHash       string
	TargetLanguage string
	VoiceProfileID string
	AudioFormat    string
}

type SynthesizeResult struct {
	AudioAssetRef string
	AudioFormat   string
	DurationMS    int64
	Provider      ProviderRef
}

type TTSProvider interface {
	Synthesize(context.Context, SynthesizeRequest) (SynthesizeResult, error)
}
