package fake

import (
	"context"
	"time"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules/identity"
)

const accessSessionLifetime = 8 * time.Hour

type IdentityService struct {
	now func() time.Time
}

func NewIdentityService(now func() time.Time) *IdentityService {
	return &IdentityService{now: now}
}

func (s *IdentityService) StartAccessSession(ctx context.Context, assertion identity.IdentityAssertion) (identity.AccessContext, error) {
	if err := ctx.Err(); err != nil {
		return identity.AccessContext{}, err
	}
	if assertion.ProviderCode != "demo" {
		return identity.AccessContext{}, identity.ErrInvalidAssertion
	}

	issuedAt := s.now().UTC()
	base := identity.AccessContext{
		OrganizationID:     "org-demo-001",
		ServicePointScopes: []string{"service-point-demo-001"},
		WindowScopes:       []string{"window-demo-001"},
		IssuedAt:           issuedAt,
		ExpiresAt:          issuedAt.Add(accessSessionLifetime),
	}

	switch assertion.ExternalSubject {
	case "staff-active":
		base.OperatorID = "staff-demo-001"
		base.MembershipID = "membership-demo-staff-001"
		base.RoleCodes = []identity.RoleCode{identity.RoleStaff}
		base.AccessSessionID = "session-demo-staff-001"
		return base, nil
	case "config-maintainer":
		base.OperatorID = "maintainer-demo-001"
		base.MembershipID = "membership-demo-maintainer-001"
		base.RoleCodes = []identity.RoleCode{identity.RoleConfigMaintainer}
		base.AccessSessionID = "session-demo-maintainer-001"
		return base, nil
	case "staff-disabled":
		return identity.AccessContext{}, identity.ErrAuthenticationFailed
	case "provider-error":
		return identity.AccessContext{}, identity.ErrProviderUnavailable
	default:
		return identity.AccessContext{}, identity.ErrInvalidAssertion
	}
}
