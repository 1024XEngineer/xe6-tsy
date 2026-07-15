package reception

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) Create(ctx *gin.Context) {
	var command CreateReceptionSessionCommand
	if !h.bind(ctx, &command) {
		return
	}
	view, err := h.service.CreateSession(ctx.Request.Context(), command)
	if err != nil {
		h.writeError(ctx, err, nil)
		return
	}
	h.writeData(ctx, http.StatusCreated, view)
}

func (h *Handler) Get(ctx *gin.Context) {
	view, err := h.service.GetSession(ctx.Request.Context(), ctx.Param("session_id"))
	if err != nil {
		h.writeError(ctx, err, nil)
		return
	}
	h.writeData(ctx, http.StatusOK, view)
}

func (h *Handler) Start(ctx *gin.Context) {
	var command StartReceptionSessionCommand
	if !h.bind(ctx, &command) {
		return
	}
	command.SessionID = ctx.Param("session_id")
	result, err := h.service.StartSession(ctx.Request.Context(), command)
	if err != nil {
		h.writeError(ctx, err, nil)
		return
	}
	h.writeData(ctx, http.StatusOK, result)
}

func (h *Handler) Attach(ctx *gin.Context) {
	var command AttachFakeMediaTrackCommand
	if !h.bind(ctx, &command) {
		return
	}
	command.SessionID = ctx.Param("session_id")
	result, err := h.service.AttachFakeMedia(ctx.Request.Context(), command)
	if err != nil {
		extra := gin.H{"data": gin.H{"binding": result.Binding}}
		if result.Degradation != nil {
			extra["degradation"] = result.Degradation
		}
		h.writeError(ctx, err, extra)
		return
	}
	h.writeData(ctx, http.StatusCreated, result)
}

func (h *Handler) Detach(ctx *gin.Context) {
	var command DetachMediaTrackCommand
	if !h.bind(ctx, &command) {
		return
	}
	command.SessionID = ctx.Param("session_id")
	command.BindingID = ctx.Param("binding_id")
	view, err := h.service.DetachMedia(ctx.Request.Context(), command)
	if err != nil {
		h.writeError(ctx, err, nil)
		return
	}
	h.writeData(ctx, http.StatusOK, view)
}

func (h *Handler) End(ctx *gin.Context) {
	var command EndReceptionSessionCommand
	if !h.bind(ctx, &command) {
		return
	}
	command.SessionID = ctx.Param("session_id")
	view, err := h.service.EndSession(ctx.Request.Context(), command)
	if err != nil {
		h.writeError(ctx, err, nil)
		return
	}
	h.writeData(ctx, http.StatusOK, view)
}

func (h *Handler) Cancel(ctx *gin.Context) {
	var command CancelReceptionSessionCommand
	if !h.bind(ctx, &command) {
		return
	}
	command.SessionID = ctx.Param("session_id")
	view, err := h.service.CancelSession(ctx.Request.Context(), command)
	if err != nil {
		h.writeError(ctx, err, nil)
		return
	}
	h.writeData(ctx, http.StatusOK, view)
}

func (h *Handler) bind(ctx *gin.Context, target any) bool {
	if err := ctx.ShouldBindJSON(target); err != nil {
		h.writeError(ctx, businessError(CodeValidationFailed, "请求 JSON 无效。"), nil)
		return false
	}
	return true
}

func (h *Handler) writeData(ctx *gin.Context, status int, data any) {
	ctx.JSON(status, gin.H{"data": data, "trace_id": h.traceID(ctx)})
}

func (h *Handler) writeError(ctx *gin.Context, err error, extra gin.H) {
	var apiError *Error
	if !errors.As(err, &apiError) {
		apiError = &Error{Code: CodeInternalError, Message: "服务暂时不可用。", Details: map[string]any{}}
	}
	status := statusForCode(apiError.Code)
	body := gin.H{
		"error":    gin.H{"code": apiError.Code, "message": apiError.Message, "retryable": apiError.Retryable, "details": apiError.Details},
		"trace_id": h.traceID(ctx),
	}
	for key, value := range extra {
		body[key] = value
	}
	ctx.JSON(status, body)
}

func (h *Handler) traceID(ctx *gin.Context) string {
	if value := ctx.GetHeader("X-Trace-ID"); value != "" {
		return value
	}
	return "trace-demo-" + h.service.ids.NewID("request")
}

func statusForCode(code string) int {
	switch code {
	case CodeValidationFailed:
		return http.StatusBadRequest
	case CodeAccessContextInvalid, CodeAccessDenied, CodeOrganizationScopeMismatch, CodeReceptionNotAllowed,
		CodeRealtimeAudioNotAllowed, CodeProcessingContextExpired:
		return http.StatusForbidden
	case CodeSessionNotFound, CodeBindingNotFound:
		return http.StatusNotFound
	case CodeInvalidSessionState, CodeInvalidBindingState, CodeVersionMismatch, CodeIdempotencyConflict, CodeActiveMediaBindingExists:
		return http.StatusConflict
	case CodeConfigNotPublished, CodeConfigVersionMismatch:
		return http.StatusConflict
	case CodeUnsupportedFakeScenario:
		return http.StatusUnprocessableEntity
	case CodeMediaAttachFailed, CodeMediaDetachFailed:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
