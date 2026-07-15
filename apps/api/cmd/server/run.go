package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/reception"
	"github.com/gin-gonic/gin"
)

type healthResponse struct {
	Status string `json:"status"`
}

func addressFromEnvironment() string {
	if value := os.Getenv("XE6_API_ADDRESS"); value != "" {
		return value
	}
	return "127.0.0.1:8080"
}

func newHandler() http.Handler {
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, healthResponse{Status: "ok"})
	})
	reception.NewModule().Register(router.Group("/api/v1"))
	return router
}

func run(ctx context.Context, address string) error {
	server := &http.Server{
		Addr:              address,
		Handler:           newHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("server shutdown failed", "error", err)
		}
	}()

	slog.Info("API listening", "address", address)
	return server.ListenAndServe()
}
