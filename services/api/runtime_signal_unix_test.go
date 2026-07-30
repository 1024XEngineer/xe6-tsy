//go:build unix

package main

import (
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestConfiguredRuntimeServeStopsOnTerminationSignal(t *testing.T) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(signals) })

	runtime := newRuntimeBlockingServeFixture(t)
	done := make(chan error, 1)
	go func() {
		done <- runtime.Serve("127.0.0.1:0", http.NewServeMux())
	}()

	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v, want graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not stop after SIGTERM")
	}
}
