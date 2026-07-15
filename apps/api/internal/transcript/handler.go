package transcript

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	service Service
}

func NewHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Register(routes gin.IRoutes) {
	routes.POST("/runs", h.startRun)
	routes.POST("/runs/:run_id/complete", h.completeRun)
	routes.GET("/sessions/:session_id/utterances", h.listUtterances)
	routes.PUT("/sessions/:session_id/speakers/:cluster_id/role", h.assignSpeakerRole)
	routes.POST("/tts", h.requestTTS)
}

func (h *Handler) startRun(c *gin.Context) {
	var cmd StartRunCommand
	if err := c.ShouldBindJSON(&cmd); err != nil {
		writeError(c, ErrInvalidInput)
		return
	}
	run, err := h.service.StartRun(c.Request.Context(), cmd)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, run)
}

func (h *Handler) completeRun(c *gin.Context) {
	run, err := h.service.CompleteRun(c.Request.Context(), c.Param("run_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, run)
}

func (h *Handler) listUtterances(c *gin.Context) {
	utterances, err := h.service.ListUtterances(c.Request.Context(), c.Param("session_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, utterances)
}

func (h *Handler) assignSpeakerRole(c *gin.Context) {
	var cmd AssignSpeakerRoleCommand
	if err := c.ShouldBindJSON(&cmd); err != nil {
		writeError(c, ErrInvalidInput)
		return
	}
	cmd.SessionID = c.Param("session_id")
	cmd.SpeakerClusterID = c.Param("cluster_id")
	binding, err := h.service.AssignSpeakerRole(c.Request.Context(), cmd)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, binding)
}

func (h *Handler) requestTTS(c *gin.Context) {
	var cmd RequestTTSCommand
	if err := c.ShouldBindJSON(&cmd); err != nil {
		writeError(c, ErrInvalidInput)
		return
	}
	run, err := h.service.RequestTTS(c.Request.Context(), cmd)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, run)
}

func writeError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	code := "INTERNAL_ERROR"
	message := "request failed"
	switch {
	case errors.Is(err, ErrInvalidInput):
		status = http.StatusBadRequest
		code = "INVALID_REQUEST"
		message = "invalid request"
	case errors.Is(err, ErrDependencyUnavailable):
		status = http.StatusServiceUnavailable
		code = "DEPENDENCY_UNAVAILABLE"
		message = "dependency unavailable"
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
