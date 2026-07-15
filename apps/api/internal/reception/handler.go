package reception

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *Service
}

type responseEnvelope[T any] struct {
	Data    T      `json:"data"`
	TraceID string `json:"trace_id"`
}

type errorView struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

type attachFailureData struct {
	Binding MediaTrackBindingView `json:"binding"`
}

type errorEnvelope struct {
	Error       errorView          `json:"error"`
	TraceID     string             `json:"trace_id"`
	Data        *attachFailureData `json:"data,omitempty"`
	Degradation *DegradationView   `json:"degradation,omitempty"`
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
	writeData(ctx, http.StatusCreated, view, h.traceID(ctx))
}

func (h *Handler) Get(ctx *gin.Context) {
	view, err := h.service.GetSession(ctx.Request.Context(), ctx.Param("session_id"))
	if err != nil {
		h.writeError(ctx, err, nil)
		return
	}
	writeData(ctx, http.StatusOK, view, h.traceID(ctx))
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
	writeData(ctx, http.StatusOK, result, h.traceID(ctx))
}

func (h *Handler) Attach(ctx *gin.Context) {
	var command AttachFakeMediaTrackCommand
	if !h.bind(ctx, &command) {
		return
	}
	command.SessionID = ctx.Param("session_id")
	result, err := h.service.AttachFakeMedia(ctx.Request.Context(), command)
	if err != nil {
		var failure *AttachFakeMediaResult
		if result.Binding.BindingID != "" || result.Degradation != nil {
			failure = &result
		}
		h.writeError(ctx, err, failure)
		return
	}
	writeData(ctx, http.StatusCreated, result, h.traceID(ctx))
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
	writeData(ctx, http.StatusOK, view, h.traceID(ctx))
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
	writeData(ctx, http.StatusOK, view, h.traceID(ctx))
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
	writeData(ctx, http.StatusOK, view, h.traceID(ctx))
}

func (h *Handler) bind(ctx *gin.Context, target any) bool {
	if err := ctx.ShouldBindJSON(target); err != nil {
		h.writeError(ctx, businessError(CodeValidationFailed, "请求 JSON 无效。"), nil)
		return false
	}
	return true
}

func writeData[T any](ctx *gin.Context, status int, data T, traceID string) {
	ctx.JSON(status, responseEnvelope[T]{Data: data, TraceID: traceID})
}

func (h *Handler) writeError(ctx *gin.Context, err error, attachFailure *AttachFakeMediaResult) {
	var apiError *Error
	if !errors.As(err, &apiError) {
		apiError = &Error{Code: CodeInternalError, Message: "服务暂时不可用。", Details: map[string]any{}}
	}
	body := errorEnvelope{
		Error: errorView{
			Code: apiError.Code, Message: apiError.Message,
			Retryable: apiError.Retryable, Details: apiError.Details,
		},
		TraceID: h.traceID(ctx),
	}
	if attachFailure != nil {
		body.Data = &attachFailureData{Binding: attachFailure.Binding}
		body.Degradation = attachFailure.Degradation
	}
	ctx.JSON(statusForCode(apiError.Code), body)
}

func (h *Handler) traceID(ctx *gin.Context) string {
	if value := ctx.GetHeader("X-Trace-ID"); value != "" {
		return value
	}
	return "trace-demo-" + h.service.ids.NewID("request")
}
