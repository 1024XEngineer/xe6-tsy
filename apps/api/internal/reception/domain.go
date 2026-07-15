package reception

import "time"

type ReceptionSessionStatus string

const (
	ReceptionSessionCreated   ReceptionSessionStatus = "created"
	ReceptionSessionActive    ReceptionSessionStatus = "active"
	ReceptionSessionEnded     ReceptionSessionStatus = "ended"
	ReceptionSessionCancelled ReceptionSessionStatus = "cancelled"
)

type MediaTrackBindingStatus string

const (
	MediaTrackPending  MediaTrackBindingStatus = "pending"
	MediaTrackAttached MediaTrackBindingStatus = "attached"
	MediaTrackDetached MediaTrackBindingStatus = "detached"
	MediaTrackFailed   MediaTrackBindingStatus = "failed"
)

const (
	FakeScenarioSuccess           = "success"
	FakeScenarioAttachFailure     = "attach_failure"
	FakeScenarioRuntimeDisconnect = "runtime_disconnect"
	FakeScenarioDetachFailure     = "detach_failure"
)

type ReceptionSession struct {
	SessionID                 string
	OperatorID                string
	OrganizationID            string
	ServicePointID            string
	ServiceWindowID           string
	OrganizationConfigVersion string
	ProcessingContextRef      string
	Status                    ReceptionSessionStatus
	Version                   int64
	CreatedAt                 time.Time
	StartedAt                 *time.Time
	EndedAt                   *time.Time
	CancelledAt               *time.Time
	CancellationReasonCode    string
}

type MediaTrackBinding struct {
	BindingID   string
	SessionID   string
	TrackRef    string
	TrackKind   string
	SourceType  string
	Provider    string
	Scenario    string
	Status      MediaTrackBindingStatus
	Version     int64
	AttachedAt  *time.Time
	DetachedAt  *time.Time
	FailedAt    *time.Time
	FailureCode string
}
