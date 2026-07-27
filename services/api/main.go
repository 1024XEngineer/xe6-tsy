package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	internalwebapi "github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	recordswebapi "github.com/1024XEngineer/xe6-tsy/services/api/webapi"
)

// main wires foundation use cases into the HTTP server and owns graceful shutdown.
func main() {
	address := os.Getenv("API_ADDR")
	if address == "" {
		address = ":8080"
	}

	mux := buildMux()

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("Lingow API listening", "address", address)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("API server stopped", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("API shutdown failed", "error", err)
			os.Exit(1)
		}
	}
}

func buildMux() *http.ServeMux {
	mux := internalwebapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases())
	languages.NewHandler().Register(mux)
	recordswebapi.NewNotImplementedHandler().Register(mux)
	return mux
}
