package reception

import (
	"context"
	"time"
)

type AuthorizeRequest struct {
	AccessContextRef string
	Action           string
	OrganizationID   string
	ServicePointID   string
	ServiceWindowID  string
}

type AccessContextView struct {
	OperatorID      string
	OrganizationID  string
	ServicePointID  string
	ServiceWindowID string
	Expired         bool
}

type PublishedConfigRequest struct {
	OrganizationID  string
	ServicePointID  string
	ServiceWindowID string
	ExpectedVersion string
}

type OrganizationConfigView struct {
	OrganizationID string
	Version        string
	Status         string
}

type ProcessingContextView struct {
	Ref                         string
	ReceptionAllowed            bool
	RealtimeAudioAllowed        bool
	RecordingPersistenceAllowed bool
	Expired                     bool
}

type AttachMediaRequest struct {
	SessionID string
	BindingID string
	TrackRef  string
	Scenario  string
}

type AttachMediaAdapterResult struct{}

type DetachMediaRequest struct {
	SessionID string
	BindingID string
	TrackRef  string
	Scenario  string
}

type AccessAuthorizer interface {
	Authorize(context.Context, AuthorizeRequest) (AccessContextView, error)
}

type OrganizationConfigReader interface {
	GetPublishedConfig(context.Context, PublishedConfigRequest) (OrganizationConfigView, error)
}

type ProcessingGate interface {
	GetProcessingContext(context.Context, string) (ProcessingContextView, error)
}

type MediaAdapter interface {
	Attach(context.Context, AttachMediaRequest) (AttachMediaAdapterResult, error)
	// Detach is idempotent so callers can safely retry after cleanup or persistence fails.
	Detach(context.Context, DetachMediaRequest) error
}

type ReceptionRepository interface {
	Create(context.Context, ReceptionSession) error
	Get(context.Context, string) (ReceptionSession, error)
	Update(context.Context, ReceptionSession, int64) error
}

type MediaBindingRepository interface {
	Create(context.Context, MediaTrackBinding) error
	Get(context.Context, string) (MediaTrackBinding, error)
	Update(context.Context, MediaTrackBinding, int64) error
	FindActiveBySession(context.Context, string) (*MediaTrackBinding, error)
}

type Clock interface{ Now() time.Time }
type IDGenerator interface{ NewID(prefix string) string }
type AuditWriter interface {
	WriteAudit(context.Context, AuditEntry) error
}
type OutboxWriter interface {
	WriteEvent(context.Context, DomainEvent) error
}
type MediaResourceCleaner interface {
	// Clean is idempotent so state persistence can be retried after external cleanup.
	Clean(context.Context, string) error
}

type IdempotencyRecord struct {
	Operation   string
	Key         string
	Fingerprint string
	Result      any
}

type Mutation struct {
	Session     *ReceptionSession
	Binding     *MediaTrackBinding
	Events      []DomainEvent
	Audits      []AuditEntry
	Idempotency *IdempotencyRecord
}

type TransactionalStore interface {
	GetSession(context.Context, string) (ReceptionSession, error)
	GetBinding(context.Context, string) (MediaTrackBinding, error)
	BindingsBySession(context.Context, string) ([]MediaTrackBinding, error)
	FindActiveBinding(context.Context, string) (*MediaTrackBinding, error)
	Replay(context.Context, string, string) (*IdempotencyRecord, error)
	Commit(context.Context, Mutation) error
}
