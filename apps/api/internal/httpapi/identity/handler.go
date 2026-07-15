package identityhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules/identity"
)

type AccessSessionStarter interface {
	StartAccessSession(context.Context, identity.IdentityAssertion) (identity.AccessContext, error)
}

type Handler struct {
	sessions AccessSessionStarter
}

func NewHandler(sessions AccessSessionStarter) *Handler {
	return &Handler{sessions: sessions}
}

func (h *Handler) Name() string {
	return "identity"
}

func (h *Handler) Register(parent *gin.RouterGroup) {
	parent.Group("/identity").POST("/access-sessions", h.startAccessSession)
}

type startAccessSessionRequest struct {
	ProviderCode string `json:"provider_code"`
	FixtureCode  string `json:"fixture_code"`
}

type accessContextResponse struct {
	OperatorID         string              `json:"operator_id"`
	OrganizationID     string              `json:"organization_id"`
	MembershipID       string              `json:"membership_id"`
	RoleCodes          []identity.RoleCode `json:"role_codes"`
	ServicePointScopes []string            `json:"service_point_scopes"`
	WindowScopes       []string            `json:"window_scopes"`
	AccessSessionID    string              `json:"access_session_id"`
	IssuedAt           time.Time           `json:"issued_at"`
	ExpiresAt          time.Time           `json:"expires_at"`
}

type accessSessionResponse struct {
	Data accessContextResponse `json:"data"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

func (h *Handler) startAccessSession(ctx *gin.Context) {
	var request startAccessSessionRequest
	if err := decodeJSON(ctx, &request); err != nil || request.ProviderCode == "" || request.FixtureCode == "" {
		writeError(ctx, http.StatusBadRequest, "INVALID_REQUEST", "请求参数无效")
		return
	}

	access, err := h.sessions.StartAccessSession(ctx.Request.Context(), identity.IdentityAssertion{
		ProviderCode:    request.ProviderCode,
		ExternalSubject: request.FixtureCode,
	})
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrInvalidAssertion):
			writeError(ctx, http.StatusBadRequest, "INVALID_REQUEST", "请求参数无效")
		case errors.Is(err, identity.ErrAuthenticationFailed):
			writeError(ctx, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "登录失败")
		case errors.Is(err, identity.ErrProviderUnavailable):
			writeError(ctx, http.StatusServiceUnavailable, "IDENTITY_PROVIDER_UNAVAILABLE", "身份服务暂时不可用")
		default:
			writeError(ctx, http.StatusInternalServerError, "INTERNAL_ERROR", "服务暂时不可用")
		}
		return
	}

	ctx.JSON(http.StatusCreated, accessSessionResponse{Data: mapAccessContext(access)})
}

func decodeJSON(ctx *gin.Context, target any) error {
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func mapAccessContext(access identity.AccessContext) accessContextResponse {
	return accessContextResponse{
		OperatorID:         access.OperatorID,
		OrganizationID:     access.OrganizationID,
		MembershipID:       access.MembershipID,
		RoleCodes:          access.RoleCodes,
		ServicePointScopes: access.ServicePointScopes,
		WindowScopes:       access.WindowScopes,
		AccessSessionID:    access.AccessSessionID,
		IssuedAt:           access.IssuedAt,
		ExpiresAt:          access.ExpiresAt,
	}
}

func writeError(ctx *gin.Context, status int, code, message string) {
	ctx.JSON(status, errorResponse{Error: errorBody{Code: code, Message: message}})
}
