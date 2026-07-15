package transcript

import (
	"context"
	"errors"

	"github.com/1024XEngineer/xe6-tsy/apps/api/pkg/speechport"
)

var (
	ErrInvalidInput          = errors.New("invalid input")
	ErrDependencyUnavailable = errors.New("dependency unavailable")
)

type Service interface {
	StartRun(context.Context, StartRunCommand) (SpeechProcessingRun, error)
	CompleteRun(context.Context, string) (SpeechProcessingRun, error)
	ListUtterances(context.Context, string) ([]SpeechUtterance, error)
	AssignSpeakerRole(context.Context, AssignSpeakerRoleCommand) (SpeakerRoleBinding, error)
	RequestTTS(context.Context, RequestTTSCommand) (TtsSynthesisRun, error)
}

type AccessContext struct {
	OperatorID     string
	OrganizationID string
}

type SessionRef struct {
	SessionID     string
	ConfigVersion string
}

type MediaTrackRef struct {
	SessionID string
	TrackID   string
}

type AccessAuthorizer interface {
	Authorize(context.Context, AccessContext, string, string) error
}

type SessionReader interface {
	GetSession(context.Context, SessionRef) error
}

type MediaTrackReader interface {
	GetTrack(context.Context, MediaTrackRef) error
}

type ASRProvider = speechport.ASRProvider
type TranslationProvider = speechport.TranslationProvider
type TTSProvider = speechport.TTSProvider
