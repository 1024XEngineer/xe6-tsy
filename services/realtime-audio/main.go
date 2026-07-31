package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const shutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Getenv, nil); err != nil {
		slog.Error("realtime-audio exited", "error", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	getenv func(string) string,
	listenAndServe func(*http.Server) error,
) error {
	cfg, err := loadProcessConfig(getenv)
	if err != nil {
		return err
	}
	handler, err := newControlPlaneHandler(cfg.TicketSecret)
	if err != nil {
		return err
	}
	server := newHTTPServer(cfg.Addr, handler)
	if listenAndServe == nil {
		listenAndServe = func(s *http.Server) error { return s.ListenAndServe() }
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("realtime-audio control-plane listening", "addr", cfg.Addr)
		err := listenAndServe(server)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		return <-errCh
	case err := <-errCh:
		return err
	}
}
