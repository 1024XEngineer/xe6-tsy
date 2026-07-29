package controlplane

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

func TestClientStartCarriesOperationAndReplays(t *testing.T) {
	fixture := newFixture(t)
	server := httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL)
	request := realtimev1.StartRequest{
		OperationID: "operation-1",
		TraceID:     "trace-1",
		StartedBy:   "account-1",
	}

	first, err := client.Start(t.Context(), "session-1", request)
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	second, err := client.Start(t.Context(), "session-1", request)
	if err != nil {
		t.Fatalf("replayed Start() error = %v", err)
	}
	if first.StartOperationID != request.OperationID ||
		second.StartOperationID != request.OperationID {
		t.Fatalf(
			"StartOperationID = %q, %q, want %q",
			first.StartOperationID, second.StartOperationID, request.OperationID,
		)
	}
	if fixture.lifecycle.starts != 1 {
		t.Fatalf("lifecycle starts = %d, want 1", fixture.lifecycle.starts)
	}
	if fixture.lifecycle.startCommand.OperationID != request.OperationID ||
		fixture.lifecycle.startCommand.TraceID != request.TraceID ||
		fixture.lifecycle.startCommand.StartedBy != request.StartedBy {
		t.Fatalf("provider command = %#v, want complete mapping", fixture.lifecycle.startCommand)
	}
}

func TestClientMapsRuntimeOwnershipConflict(t *testing.T) {
	fixture := newFixture(t)
	server := httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL)

	if _, err := client.Start(t.Context(), "session-1", realtimev1.StartRequest{
		OperationID: "operation-1",
	}); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	fixture.lifecycle.startErr = session.ErrRuntimeOperationConflict
	_, err := client.Start(t.Context(), "session-1", realtimev1.StartRequest{
		OperationID: "operation-2",
	})
	if !errors.Is(err, ErrRuntimeOperationConflict) {
		t.Fatalf("conflicting Start() error = %v, want ErrRuntimeOperationConflict", err)
	}
}

func TestClientKeepsGenericConflictDistinct(t *testing.T) {
	fixture := newFixture(t)
	fixture.lifecycle.startErr = session.ErrRuntimeCleanupRequired
	server := httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL)

	_, err := client.Start(t.Context(), "session-1", realtimev1.StartRequest{
		OperationID: "operation-1",
	})
	if !errors.Is(err, ErrClientConflict) {
		t.Fatalf("Start() error = %v, want ErrClientConflict", err)
	}
	if errors.Is(err, ErrRuntimeOperationConflict) {
		t.Fatalf("Start() error = %v, must not match ErrRuntimeOperationConflict", err)
	}
}

func TestClientRejectsEmptyOrMismatchedOperation(t *testing.T) {
	fixture := newFixture(t)
	server := httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL)

	if _, err := client.Start(
		t.Context(), "session-1", realtimev1.StartRequest{},
	); !errors.Is(err, ErrClientRequest) {
		t.Fatalf("empty operation Start() error = %v, want ErrClientRequest", err)
	}
	if fixture.lifecycle.starts != 0 {
		t.Fatalf("lifecycle starts = %d, want 0", fixture.lifecycle.starts)
	}

	fixture.lifecycle.runtime.StartOperationID = "operation-2"
	if _, err := client.Start(t.Context(), "session-1", realtimev1.StartRequest{
		OperationID: "operation-1",
	}); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("mismatched operation Start() error = %v, want ErrInvalidResponse", err)
	}
}

func TestClientPreservesContextErrors(t *testing.T) {
	tests := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		want    error
	}{
		{
			name: "canceled",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			want: context.Canceled,
		},
		{
			name: "deadline",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			},
			want: context.DeadlineExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.context()
			defer cancel()
			client, err := NewClient(ClientConfig{
				BaseURL: "https://realtime.example",
				HTTP:    http.DefaultClient,
				Tickets: TicketSourceFunc(func(ctx context.Context, _ string) (string, error) {
					return "", ctx.Err()
				}),
			})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			if _, err := client.Start(ctx, "session-1", realtimev1.StartRequest{
				OperationID: "operation-1",
			}); !errors.Is(err, test.want) {
				t.Fatalf("Start() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNewClientValidatesDependencies(t *testing.T) {
	valid := ClientConfig{
		BaseURL: "https://realtime.example",
		HTTP:    http.DefaultClient,
		Tickets: TicketSourceFunc(func(context.Context, string) (string, error) {
			return "ticket", nil
		}),
	}
	tests := []struct {
		name string
		edit func(*ClientConfig)
	}{
		{name: "base URL", edit: func(config *ClientConfig) { config.BaseURL = "" }},
		{name: "HTTP client", edit: func(config *ClientConfig) { config.HTTP = nil }},
		{name: "ticket source", edit: func(config *ClientConfig) { config.Tickets = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.edit(&config)
			if _, err := NewClient(config); !errors.Is(err, ErrClientDependency) {
				t.Fatalf("NewClient() error = %v, want ErrClientDependency", err)
			}
		})
	}
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		BaseURL: baseURL,
		HTTP:    http.DefaultClient,
		Tickets: TicketSourceFunc(func(context.Context, string) (string, error) {
			return "realtime-ticket", nil
		}),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
