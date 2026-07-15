package main

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/transcript"
)

// buildApplication wires deterministic local dependencies so the skeleton
// starts without real providers, persistence, or a cross-module workflow.
func buildApplication() *gin.Engine {
	service := transcript.NewFakeService()
	return newRouter(func(router *gin.Engine) {
		transcript.NewHandler(service).Register(router.Group("/api/v1/speech"))
	})
}

func serverAddress(port string) string {
	if port == "" {
		port = "8080"
	}
	return ":" + port
}

func run(addr string) error {
	return http.ListenAndServe(addr, buildApplication())
}

func newRouter(register func(*gin.Engine)) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	if register != nil {
		register(router)
	}
	return router
}
