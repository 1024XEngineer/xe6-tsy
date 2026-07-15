package identityhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules/identity"
)

type accessSessionStarterStub struct {
	result identity.AccessContext
	err    error
	calls  int
	got    identity.IdentityAssertion
}

func (s *accessSessionStarterStub) StartAccessSession(_ context.Context, assertion identity.IdentityAssertion) (identity.AccessContext, error) {
	s.calls++
	s.got = assertion
	return s.result, s.err
}

func TestHandler_StartAccessSession(t *testing.T) {
	fixedNow := time.Date(2026, time.July, 15, 8, 0, 0, 0, time.UTC)
	success := identity.AccessContext{
		OperatorID:         "staff-demo-001",
		OrganizationID:     "org-demo-001",
		MembershipID:       "membership-demo-staff-001",
		RoleCodes:          []identity.RoleCode{identity.RoleStaff},
		ServicePointScopes: []string{"service-point-demo-001"},
		WindowScopes:       []string{"window-demo-001"},
		AccessSessionID:    "session-demo-staff-001",
		IssuedAt:           fixedNow,
		ExpiresAt:          fixedNow.Add(8 * time.Hour),
	}

	tests := []struct {
		name          string
		body          string
		result        identity.AccessContext
		dependencyErr error
		wantStatus    int
		wantCode      string
		wantCalls     int
	}{
		{
			name:       "created",
			body:       `{"provider_code":"demo","fixture_code":"staff-active"}`,
			result:     success,
			wantStatus: http.StatusCreated,
			wantCalls:  1,
		},
		{
			name:       "missing fixture",
			body:       `{"provider_code":"demo"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REQUEST",
		},
		{
			name:       "malformed json",
			body:       `{"provider_code":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_REQUEST",
		},
		{
			name:          "unknown fixture",
			body:          `{"provider_code":"demo","fixture_code":"unknown"}`,
			dependencyErr: identity.ErrInvalidAssertion,
			wantStatus:    http.StatusBadRequest,
			wantCode:      "INVALID_REQUEST",
			wantCalls:     1,
		},
		{
			name:          "disabled staff",
			body:          `{"provider_code":"demo","fixture_code":"staff-disabled"}`,
			dependencyErr: identity.ErrAuthenticationFailed,
			wantStatus:    http.StatusUnauthorized,
			wantCode:      "AUTHENTICATION_FAILED",
			wantCalls:     1,
		},
		{
			name:          "provider unavailable",
			body:          `{"provider_code":"demo","fixture_code":"provider-error"}`,
			dependencyErr: identity.ErrProviderUnavailable,
			wantStatus:    http.StatusServiceUnavailable,
			wantCode:      "IDENTITY_PROVIDER_UNAVAILABLE",
			wantCalls:     1,
		},
		{
			name:          "unexpected dependency error",
			body:          `{"provider_code":"demo","fixture_code":"staff-active"}`,
			dependencyErr: errors.New("unexpected"),
			wantStatus:    http.StatusInternalServerError,
			wantCode:      "INTERNAL_ERROR",
			wantCalls:     1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &accessSessionStarterStub{result: test.result, err: test.dependencyErr}
			router := newTestRouter(stub)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/identity/access-sessions", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if stub.calls != test.wantCalls {
				t.Fatalf("dependency calls = %d, want %d", stub.calls, test.wantCalls)
			}
			if test.wantCalls == 1 && (stub.got.ProviderCode != "demo" || stub.got.ExternalSubject == "") {
				t.Fatalf("assertion = %#v", stub.got)
			}
			if test.wantCode != "" {
				assertErrorCode(t, response, test.wantCode)
				return
			}

			var got accessSessionResponse
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got.Data.AccessSessionID != success.AccessSessionID || got.Data.RoleCodes[0] != identity.RoleStaff {
				t.Fatalf("response data = %#v", got.Data)
			}
		})
	}
}

func newTestRouter(starter AccessSessionStarter) http.Handler {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewHandler(starter).Register(router.Group("/api/v1"))
	return router
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, want string) {
	t.Helper()
	var got errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if got.Error.Code != want {
		t.Fatalf("error code = %q, want %q", got.Error.Code, want)
	}
}
