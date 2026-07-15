package httpserver

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/config"
	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules"
)

func New(cfg config.Config, registrars []modules.Registrar) http.Handler {
	gin.SetMode(cfg.Mode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/healthz", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := router.Group("/api/v1")
	for _, registrar := range registrars {
		registrar.Register(api)
	}
	return router
}
