package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/localruntime"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

const (
	defaultAddr               = ":8090"
	minTicketSecretBytes      = 32
	httpReadHeaderTimeout     = 5 * time.Second
	httpReadTimeout           = 30 * time.Second
	httpWriteTimeout          = 45 * time.Second
	httpIdleTimeout           = 60 * time.Second
	shutdownTimeout           = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("realtime-audio exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr := strings.TrimSpace(os.Getenv("REALTIME_ADDR"))
	if addr == "" {
		addr = defaultAddr
	}
	ticketSecret := strings.TrimSpace(os.Getenv("REALTIME_TICKET_SECRET"))
	if len([]byte(ticketSecret)) < minTicketSecretBytes {
		return fmt.Errorf("REALTIME_TICKET_SECRET must contain at least %d bytes", minTicketSecretBytes)
	}

	handler, err := newControlPlaneHandler(ticketSecret)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("realtime-audio control-plane listening", "addr", addr)
		err := server.ListenAndServe()
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

func newControlPlaneHandler(ticketSecret string) (http.Handler, error) {
	now := func() time.Time { return time.Now().UTC() }

	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte(ticketSecret),
		TTL:    time.Minute,
		Now:    now,
	})
	if err != nil {
		return nil, fmt.Errorf("configure ticket codec: %w", err)
	}
	tickets, err := webrtc.NewHMACTicketValidator(codec)
	if err != nil {
		return nil, fmt.Errorf("configure ticket validator: %w", err)
	}

	factory, err := webrtc.NewPionTransportFactory(webrtc.PionTransportConfig{
		ICEServers: []webrtc.ICEServerConfig{{
			URLs: []string{"stun:stun.l.google.com:19302"},
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("configure pion transport factory: %w", err)
	}
	connections := webrtc.NewMemoryConnectionManager(factory)

	signaling, err := webrtc.NewSignalingService(webrtc.Dependencies{
		Tickets:     tickets,
		Connections: connections,
		Now:         now,
	})
	if err != nil {
		return nil, fmt.Errorf("configure signaling: %w", err)
	}

	lifecycle, err := session.NewLifecycleService(session.Dependencies{
		Sessions:    localruntime.TrustSessionReader{},
		Runtimes:    session.NewMemoryRuntimeRepository(),
		Pipelines:   localruntime.NoopPipeline{},
		Connections: connections,
		Now:         now,
	})
	if err != nil {
		return nil, fmt.Errorf("configure lifecycle: %w", err)
	}

	handler, err := controlplane.New(controlplane.Dependencies{
		Lifecycle:   lifecycle,
		Signaling:   signaling,
		Connections: connections,
		Tickets:     tickets,
		Config: localruntime.StaticWebRTCConfig{
			ICEServers: []controlplane.ICEServer{{
				URLs: []string{"stun:stun.l.google.com:19302"},
			}},
			Now: now,
		},
		Now: now,
	})
	if err != nil {
		return nil, fmt.Errorf("configure control-plane: %w", err)
	}
	return handler, nil
}
