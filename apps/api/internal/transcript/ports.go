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

// Service defines Module 4 commands and read models. Implementations own
// speech state only and interact with other modules through immutable refs.
type Service interface {
	StartRun(context.Context, StartRunCommand) (SpeechProcessingRun, error)
	CompleteRun(context.Context, string) (SpeechProcessingRun, error)
	ListUtterances(context.Context, string) ([]SpeechUtterance, error)
	AssignSpeakerRole(context.Context, AssignSpeakerRoleCommand) (SpeakerRoleBinding, error)
	RequestTTS(context.Context, RequestTTSCommand) (TtsSynthesisRun, error)
}

// AccessContext is the minimal access result supplied by Module 1; it contains
// references rather than credentials or reception content.
type AccessContext struct {
	OperatorID     string
	OrganizationID string
}

// SessionRef identifies the Module 3 session and fixed configuration version
// that Module 4 must validate before starting work.
type SessionRef struct {
	SessionID     string
	ConfigVersion string
}

// MediaTrackRef identifies a Module 3 media track without transferring audio
// ownership or lifecycle control to Module 4.
type MediaTrackRef struct {
	SessionID string
	TrackID   string
}

// AccessAuthorizer is the narrow Module 1 dependency used before protected
// operations; policy and state remain outside Module 4.
type AccessAuthorizer interface {
	Authorize(context.Context, AccessContext, string, string) error
}

// SessionReader is Module 4's read-only boundary to Module 3 session state.
type SessionReader interface {
	GetSession(context.Context, SessionRef) error
}

// MediaTrackReader is Module 4's read-only boundary to Module 3 media state.
type MediaTrackReader interface {
	GetTrack(context.Context, MediaTrackRef) error
}

type ASRProvider = speechport.ASRProvider
type TranslationProvider = speechport.TranslationProvider
type TTSProvider = speechport.TTSProvider
