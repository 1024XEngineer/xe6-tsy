package identity

import (
	"context"
	"time"
)

type RoleCode string

const (
	RoleStaff            RoleCode = "STAFF"
	RoleConfigMaintainer RoleCode = "CONFIG_MAINTAINER"
)

type ScopeType string

const (
	ScopeOrganization ScopeType = "ORGANIZATION"
	ScopeServicePoint ScopeType = "SERVICE_POINT"
	ScopeWindow       ScopeType = "WINDOW"
)

type Decision string

const (
	DecisionAllow Decision = "ALLOW"
	DecisionDeny  Decision = "DENY"
)

// Service defines the MS1 identity and access use cases. Implementations are
// intentionally deferred to later contract, schema, and engineering issues.
type Service interface {
	StartAccessSession(context.Context, IdentityAssertion) (AccessContext, error)
	GetAccessContext(context.Context, GetAccessContextQuery) (AccessContext, error)
	AuthorizeAction(context.Context, AuthorizationRequest) (AccessDecision, error)
	EndAccessSession(context.Context, EndAccessSessionCommand) error
	RevokeMembership(context.Context, RevokeMembershipCommand) error
	RevokeAccessSession(context.Context, RevokeAccessSessionCommand) error
}

type IdentityAssertion struct {
	ProviderCode    string
	ExternalSubject string
	IssuedAt        time.Time
	ExpiresAt       time.Time
	Nonce           string
}

type GetAccessContextQuery struct {
	AccessSessionID string
	OrganizationID  string
}

type AuthorizationRequest struct {
	AccessSessionID string
	Action          string
	OrganizationID  string
	ResourceType    string
	ResourceID      string
	ServicePointID  string
	WindowID        string
}

type AccessContext struct {
	OperatorID         string
	OrganizationID     string
	MembershipID       string
	RoleCodes          []RoleCode
	ServicePointScopes []string
	WindowScopes       []string
	AccessSessionID    string
	IssuedAt           time.Time
	ExpiresAt          time.Time
}

type AccessDecision struct {
	Decision      Decision
	ReasonCode    string
	PolicyVersion string
	EvaluatedAt   time.Time
}

type EndAccessSessionCommand struct {
	AccessSessionID string
	ExpectedVersion int64
}

type RevokeMembershipCommand struct {
	MembershipID    string
	ActorID         string
	Reason          string
	ExpectedVersion int64
}

type RevokeAccessSessionCommand struct {
	AccessSessionID string
	ActorID         string
	Reason          string
	ExpectedVersion int64
}
