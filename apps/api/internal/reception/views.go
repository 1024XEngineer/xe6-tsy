package reception

import "time"

type MediaCapability struct {
	AudioTrackAllowed   bool `json:"audio_track_allowed"`
	ManualTextAvailable bool `json:"manual_text_available"`
}

type DegradationView struct {
	Mode                 string `json:"mode"`
	SessionRemainsActive bool   `json:"session_remains_active"`
	ReasonCode           string `json:"reason_code"`
}

type MediaTrackBindingView struct {
	BindingID   string                  `json:"binding_id"`
	SessionID   string                  `json:"session_id"`
	TrackRef    string                  `json:"track_ref"`
	TrackKind   string                  `json:"track_kind"`
	SourceType  string                  `json:"source_type"`
	Provider    string                  `json:"provider"`
	Scenario    string                  `json:"scenario"`
	Status      MediaTrackBindingStatus `json:"status"`
	Version     int64                   `json:"version"`
	AttachedAt  *time.Time              `json:"attached_at,omitempty"`
	DetachedAt  *time.Time              `json:"detached_at,omitempty"`
	FailedAt    *time.Time              `json:"failed_at,omitempty"`
	FailureCode string                  `json:"failure_code,omitempty"`
}

type ReceptionSessionView struct {
	SessionID                 string                  `json:"session_id"`
	OperatorID                string                  `json:"operator_id"`
	OrganizationID            string                  `json:"organization_id"`
	ServicePointID            string                  `json:"service_point_id"`
	ServiceWindowID           string                  `json:"service_window_id"`
	OrganizationConfigVersion string                  `json:"organization_config_version"`
	ProcessingContextRef      string                  `json:"processing_context_ref"`
	Status                    ReceptionSessionStatus  `json:"status"`
	Version                   int64                   `json:"version"`
	CreatedAt                 time.Time               `json:"created_at"`
	StartedAt                 *time.Time              `json:"started_at,omitempty"`
	EndedAt                   *time.Time              `json:"ended_at,omitempty"`
	CancelledAt               *time.Time              `json:"cancelled_at,omitempty"`
	CancellationReasonCode    string                  `json:"cancellation_reason_code,omitempty"`
	MediaCapability           MediaCapability         `json:"media_capability"`
	MediaBindings             []MediaTrackBindingView `json:"media_bindings"`
}

type StartReceptionSessionResult struct {
	Session         ReceptionSessionView `json:"session"`
	MediaCapability MediaCapability      `json:"media_capability"`
}

type AttachFakeMediaResult struct {
	Binding     MediaTrackBindingView `json:"binding"`
	Degradation *DegradationView      `json:"degradation,omitempty"`
}

func sessionView(session ReceptionSession, bindings []MediaTrackBinding, capability MediaCapability) ReceptionSessionView {
	views := make([]MediaTrackBindingView, 0, len(bindings))
	for _, binding := range bindings {
		views = append(views, bindingView(binding))
	}
	return ReceptionSessionView{
		SessionID: session.SessionID, OperatorID: session.OperatorID,
		OrganizationID: session.OrganizationID, ServicePointID: session.ServicePointID,
		ServiceWindowID: session.ServiceWindowID, OrganizationConfigVersion: session.OrganizationConfigVersion,
		ProcessingContextRef: session.ProcessingContextRef, Status: session.Status, Version: session.Version,
		CreatedAt: session.CreatedAt, StartedAt: session.StartedAt, EndedAt: session.EndedAt,
		CancelledAt: session.CancelledAt, CancellationReasonCode: session.CancellationReasonCode,
		MediaCapability: capability, MediaBindings: views,
	}
}

func bindingView(binding MediaTrackBinding) MediaTrackBindingView {
	return MediaTrackBindingView{
		BindingID: binding.BindingID, SessionID: binding.SessionID, TrackRef: binding.TrackRef,
		TrackKind: binding.TrackKind, SourceType: binding.SourceType, Provider: binding.Provider,
		Scenario: binding.Scenario, Status: binding.Status, Version: binding.Version,
		AttachedAt: binding.AttachedAt, DetachedAt: binding.DetachedAt, FailedAt: binding.FailedAt,
		FailureCode: binding.FailureCode,
	}
}
