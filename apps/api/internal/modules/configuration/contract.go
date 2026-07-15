package configuration

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules/identity"
)

// Service defines the MS1 organization configuration use cases. It contains
// no persistence, validation, publication, or authorization implementation.
type Service interface {
	CreateConfigDraft(context.Context, CreateConfigDraftCommand) (ConfigDraftRef, error)
	UpdateConfigDraft(context.Context, UpdateConfigDraftCommand) (ConfigDraftRef, error)
	ValidateConfigDraft(context.Context, ValidateConfigDraftCommand) (ConfigValidationResult, error)
	PublishConfig(context.Context, PublishConfigCommand) (PublishedOrganizationConfigSnapshot, error)
	GetConfigVersion(context.Context, GetConfigVersionQuery) (PublishedOrganizationConfigSnapshot, error)
	ResolveEffectiveOrganizationConfig(context.Context, ResolveEffectiveConfigQuery) (PublishedOrganizationConfigSnapshot, error)
	RetireConfigVersion(context.Context, RetireConfigVersionCommand) error
}

// KnowledgeService keeps the knowledge ingestion and publication methods owned
// by module 2, separate from runtime retrieval owned by module 6.
type KnowledgeService interface {
	CreateKnowledgeImportJob(context.Context, CreateKnowledgeImportJobCommand) (KnowledgeImportResult, error)
	ReviewKnowledgeImportItem(context.Context, ReviewKnowledgeImportItemCommand) error
	CreateKnowledgePublication(context.Context, CreateKnowledgePublicationCommand) (PublishedKnowledgeBaseRef, error)
	GetPublishedKnowledgeBundle(context.Context, GetPublishedKnowledgeBundleQuery) (PublishedKnowledgeBundle, error)
	RetirePublication(context.Context, RetirePublicationCommand) error
}

// AccessControl is module 2's required module 1 boundary for every write command.
type AccessControl interface {
	AuthorizeAction(context.Context, identity.AuthorizationRequest) (identity.AccessDecision, error)
}

type ConfigDraftInput struct {
	OrganizationID         string
	RegionCodes            []string
	ServicePointIDs        []string
	WindowIDs              []string
	SupportedDialects      []string
	LocalExpressions       []string
	RecordTemplate         map[string]any
	KnowledgePublicationID string
}

type CreateConfigDraftCommand struct {
	Actor          identity.AccessContext
	Input          ConfigDraftInput
	IdempotencyKey string
}

type UpdateConfigDraftCommand struct {
	Actor           identity.AccessContext
	DraftID         string
	Input           ConfigDraftInput
	ExpectedVersion int64
}

type ValidateConfigDraftCommand struct {
	Actor           identity.AccessContext
	DraftID         string
	ExpectedVersion int64
}

type PublishConfigCommand struct {
	Actor           identity.AccessContext
	DraftID         string
	ValidationRunID string
	ExpectedVersion int64
	IdempotencyKey  string
}

type GetConfigVersionQuery struct {
	OrganizationID  string
	ConfigVersionID string
}

type ResolveEffectiveConfigQuery struct {
	OrganizationID string
}

type RetireConfigVersionCommand struct {
	Actor           identity.AccessContext
	ConfigVersionID string
	Reason          string
	ExpectedVersion int64
}

type ConfigDraftRef struct {
	DraftID        string
	OrganizationID string
	Status         string
	RowVersion     int64
}

type ConfigValidationItem struct {
	Severity  string
	Code      string
	FieldPath string
	Message   string
}

type ConfigValidationResult struct {
	ValidationRunID string
	DraftID         string
	Status          string
	Items           []ConfigValidationItem
}

type PublishedOrganizationConfigSnapshot struct {
	OrganizationID         string
	ConfigVersionID        string
	VersionNumber          int
	ServicePointIDs        []string
	WindowIDs              []string
	RegionCodes            []string
	SupportedDialects      []string
	LocalExpressions       []string
	RecordTemplate         map[string]any
	KnowledgePublicationID string
}

type KnowledgeInputMode string

const (
	KnowledgeInputForm KnowledgeInputMode = "FORM"
	KnowledgeInputFile KnowledgeInputMode = "FILE"
)

type CreateKnowledgeImportJobCommand struct {
	Actor           identity.AccessContext
	OrganizationID  string
	KnowledgeBaseID string
	InputMode       KnowledgeInputMode
	SourceFileRef   string
	SourceSHA256    string
	IdempotencyKey  string
}

type ReviewKnowledgeImportItemCommand struct {
	Actor            identity.AccessContext
	ImportItemID     string
	Decision         string
	ReasonCode       string
	CorrectedPayload map[string]any
	ExpectedVersion  int64
}

type CreateKnowledgePublicationCommand struct {
	Actor           identity.AccessContext
	OrganizationID  string
	KnowledgeBaseID string
	EntryVersionIDs []string
	IdempotencyKey  string
}

type GetPublishedKnowledgeBundleQuery struct {
	OrganizationID  string
	KnowledgeBaseID string
	PublicationID   string
	VersionNumber   int
}

type RetirePublicationCommand struct {
	Actor           identity.AccessContext
	PublicationID   string
	Reason          string
	ExpectedVersion int64
}

type KnowledgeImportResult struct {
	ImportJobID     string
	Status          string
	AcceptedCount   int
	WarningCount    int
	RejectedCount   int
	ValidationItems []ConfigValidationItem
}

type PublishedKnowledgeBaseRef struct {
	OrganizationID           string
	KnowledgeBaseID          string
	PublicationID            string
	PublicationVersionNumber int
	ManifestHash             string
	ApplicableScope          map[string]any
}

type PublishedKnowledgeBundle struct {
	Publication PublishedKnowledgeBaseRef
	Entries     []PublishedKnowledgeEntry
}

type PublishedKnowledgeEntry struct {
	EntryVersionID string
	MatterName     string
	Content        map[string]any
	SourceRefs     []string
}
