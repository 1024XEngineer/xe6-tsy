package transcript

import (
	"time"

	"github.com/1024XEngineer/xe6-tsy/apps/api/pkg/speechport"
)

// Direction states which side of a reception session produced the source
// speech, allowing both language directions to retain their original text.
type Direction string

const (
	DirectionCitizenToWorker Direction = "citizen_to_worker"
	DirectionWorkerToCitizen Direction = "worker_to_citizen"
)

// TTSMode separates disabled, worker-triggered, and automatic synthesis
// without changing an immutable transcript result.
type TTSMode string

const (
	TTSModeOff    TTSMode = "off"
	TTSModeManual TTSMode = "manual"
	TTSModeAuto   TTSMode = "auto"
)

// RunStatus represents the lifecycle exposed by this skeleton. Recovery and
// persistence states belong to a later workflow implementation.
type RunStatus string

const (
	RunStatusStreaming RunStatus = "streaming"
	RunStatusCompleted RunStatus = "completed"
)

// SpeakerRole is a session-local generalized label and must never encode a
// person's real-world identity.
type SpeakerRole string

const (
	SpeakerRoleCitizen SpeakerRole = "citizen"
	SpeakerRoleWorker  SpeakerRole = "worker"
	SpeakerRoleFamily  SpeakerRole = "family"
	SpeakerRoleOther   SpeakerRole = "other"
	SpeakerRoleUnknown SpeakerRole = "unknown"
)

// TTSStatus represents the initial asynchronous synthesis state reserved by
// this skeleton; ready and failed transitions are not implemented here.
type TTSStatus string

const (
	TTSStatusPending TTSStatus = "pending"
)

// StartRunCommand fixes access, configuration, session, and media-track
// references without taking ownership of upstream entities.
type StartRunCommand struct {
	OperatorID     string    `json:"operator_id"`
	OrganizationID string    `json:"organization_id"`
	ConfigVersion  string    `json:"organization_config_version"`
	SessionID      string    `json:"session_id"`
	TrackID        string    `json:"track_id"`
	Direction      Direction `json:"direction"`
	SourceLanguage string    `json:"source_language"`
	TargetLanguage string    `json:"target_language"`
	TTSMode        TTSMode   `json:"tts_mode"`
	VoiceProfileID string    `json:"voice_profile_id,omitempty"`
}

func (c StartRunCommand) valid() bool {
	return c.OperatorID != "" && c.OrganizationID != "" && c.ConfigVersion != "" &&
		c.SessionID != "" && c.TrackID != "" && c.Direction != "" &&
		c.SourceLanguage != "" && c.TargetLanguage != "" && c.TTSMode != ""
}

// SpeechProcessingRun is Module 4's authoritative record for one typed media
// processing lifecycle. It references, but never mutates, upstream entities.
type SpeechProcessingRun struct {
	ID             string    `json:"run_id"`
	SessionID      string    `json:"session_id"`
	TrackID        string    `json:"track_id"`
	ConfigVersion  string    `json:"organization_config_version"`
	Direction      Direction `json:"direction"`
	SourceLanguage string    `json:"source_language"`
	TargetLanguage string    `json:"target_language"`
	TTSMode        TTSMode   `json:"tts_mode"`
	Status         RunStatus `json:"status"`
	StartedAt      time.Time `json:"started_at"`
}

// SpeechUtterance is the immutable final text artifact. Source and translated
// text remain distinct so consumers can inspect their separate origins.
type SpeechUtterance struct {
	ID                    string                 `json:"utterance_id"`
	SessionID             string                 `json:"session_id"`
	TrackID               string                 `json:"track_id"`
	SequenceNo            int64                  `json:"sequence_no"`
	StartMS               int64                  `json:"start_ms"`
	EndMS                 int64                  `json:"end_ms"`
	Direction             Direction              `json:"direction"`
	SpeakerClusterID      string                 `json:"speaker_cluster_id"`
	SourceLanguage        string                 `json:"source_language"`
	TargetLanguage        string                 `json:"target_language"`
	SourceText            string                 `json:"source_text"`
	TranslatedText        *string                `json:"translated_text"`
	ASRConfidence         *float64               `json:"asr_confidence"`
	TranslationConfidence *float64               `json:"translation_confidence"`
	ASRProvider           speechport.ProviderRef `json:"asr_provider"`
	TranslationProvider   speechport.ProviderRef `json:"translation_provider"`
}

// AssignSpeakerRoleCommand updates only a generalized session-local role and
// cannot attach a speaker cluster to an identifiable person.
type AssignSpeakerRoleCommand struct {
	OperatorID       string      `json:"operator_id"`
	OrganizationID   string      `json:"organization_id"`
	SessionID        string      `json:"session_id"`
	SpeakerClusterID string      `json:"speaker_cluster_id"`
	Role             SpeakerRole `json:"role"`
}

// SpeakerRoleBinding records the current generalized role for one anonymous
// speaker cluster within a single session.
type SpeakerRoleBinding struct {
	SessionID        string      `json:"session_id"`
	SpeakerClusterID string      `json:"speaker_cluster_id"`
	Role             SpeakerRole `json:"role"`
	UpdatedBy        string      `json:"updated_by"`
}

// RequestTTSCommand reserves a synthesis task for a referenced text snapshot
// and intentionally avoids modifying the source transcript.
type RequestTTSCommand struct {
	OperatorID     string `json:"operator_id"`
	OrganizationID string `json:"organization_id"`
	SessionID      string `json:"session_id"`
	SourceRef      string `json:"source_ref"`
	TextHash       string `json:"source_text_hash"`
	TargetLanguage string `json:"target_language"`
	VoiceProfileID string `json:"voice_profile_id"`
}

// TtsSynthesisRun is Module 4's task reference for asynchronously generated
// audio. The actual audio bytes remain owned by a media capability.
type TtsSynthesisRun struct {
	ID             string    `json:"tts_run_id"`
	SessionID      string    `json:"session_id"`
	SourceRef      string    `json:"source_ref"`
	TextHash       string    `json:"source_text_hash"`
	TargetLanguage string    `json:"target_language"`
	VoiceProfileID string    `json:"voice_profile_id"`
	Status         TTSStatus `json:"status"`
}
