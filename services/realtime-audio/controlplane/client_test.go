package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
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

func TestClientGetConnectionMapsAllStates(t *testing.T) {
	for _, state := range []realtimev1.ConnectionState{
		realtimev1.ConnectionNew,
		realtimev1.ConnectionConnecting,
		realtimev1.ConnectionConnected,
		realtimev1.ConnectionDisconnected,
		realtimev1.ConnectionFailed,
		realtimev1.ConnectionClosed,
	} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newFixture(t)
			fixture.connections.snapshot.State = state
			server := httptest.NewServer(fixture.handler)
			t.Cleanup(server.Close)

			snapshot, err := newTestClient(t, server.URL).GetConnection(
				t.Context(),
				"session-1",
			)
			if err != nil {
				t.Fatalf("GetConnection() error = %v", err)
			}
			if snapshot.SessionID != "session-1" ||
				snapshot.ConnectionID != "connection-1" ||
				snapshot.State != state ||
				snapshot.UpdatedAt.IsZero() {
				t.Fatalf("GetConnection() = %#v", snapshot)
			}
		})
	}
}

func TestClientStopCarriesReasonTimeAndReplaysByReason(t *testing.T) {
	fixture := newFixture(t)
	server := httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL)
	endedAt := time.Unix(1700000060, 0).UTC()

	first, err := client.Stop(t.Context(), "session-1", realtimev1.StopRequest{
		TraceID: "trace-stop-1",
		Reason:  "user_requested",
		EndedAt: endedAt,
	})
	if err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	second, err := client.Stop(t.Context(), "session-1", realtimev1.StopRequest{
		TraceID: "trace-stop-2",
		Reason:  "user_requested",
		EndedAt: endedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("replayed Stop() error = %v", err)
	}
	if first.RuntimeState != realtimev1.RuntimeStopped ||
		second.RuntimeState != realtimev1.RuntimeStopped {
		t.Fatalf("Stop() states = %q, %q", first.RuntimeState, second.RuntimeState)
	}
	if fixture.lifecycle.stops != 1 {
		t.Fatalf("lifecycle stops = %d, want 1", fixture.lifecycle.stops)
	}
	if fixture.lifecycle.stopCommand.TraceID != "trace-stop-1" ||
		fixture.lifecycle.stopCommand.Reason != "user_requested" ||
		!fixture.lifecycle.stopCommand.EndedAt.Equal(endedAt) {
		t.Fatalf("stop command = %#v, want first request mapping", fixture.lifecycle.stopCommand)
	}
}

func TestClientPlayFallbackAcceptsIdempotentReceipts(t *testing.T) {
	statuses := []realtimev1.FallbackPlaybackReceiptStatus{
		realtimev1.FallbackPlaybackAccepted,
		realtimev1.FallbackPlaybackAlreadyAccepted,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/realtime/v1/sessions/session-1/fallback-playback" || request.Header.Get("Idempotency-Key") != "fallback:fallback-1" {
					t.Fatalf("request path/header = %q, %q", request.URL.Path, request.Header.Get("Idempotency-Key"))
				}
				writer.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(writer).Encode(realtimev1.FallbackPlaybackReceipt{OperationID: "fallback-1", Status: status})
			}))
			defer server.Close()
			client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTP: server.Client(), Tickets: TicketSourceFunc(func(context.Context, string) (string, error) { return "ticket", nil })})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			receipt, err := client.PlayFallback(t.Context(), "session-1", realtimev1.FallbackPlaybackRequest{
				OperationID: "fallback-1", SessionID: "session-1", TurnID: "turn-1", TargetLanguage: "zh-CN",
				TranslatedText: "translated", LanguageConfigVersion: 3, TraceID: "trace-1",
			})
			if err != nil || receipt.Status != status {
				t.Fatalf("PlayFallback() = %#v, %v", receipt, err)
			}
		})
	}
}

func TestClientPlayFallbackValidatesSnapshotAndTicket(t *testing.T) {
	valid := realtimev1.FallbackPlaybackRequest{
		OperationID: "fallback-1", SessionID: "session-1", TurnID: "turn-1", TargetLanguage: "zh-CN",
		TranslatedText: "translated", LanguageConfigVersion: 3, TraceID: "trace-1",
	}
	client := newTestClient(t, "https://realtime.example")
	tests := []struct {
		name string
		edit func(*realtimev1.FallbackPlaybackRequest, *string)
	}{
		{name: "empty session", edit: func(request *realtimev1.FallbackPlaybackRequest, sessionID *string) { *sessionID = "" }},
		{name: "operation missing", edit: func(request *realtimev1.FallbackPlaybackRequest, _ *string) { request.OperationID = "" }},
		{name: "session mismatch", edit: func(request *realtimev1.FallbackPlaybackRequest, _ *string) { request.SessionID = "other-session" }},
		{name: "translation missing", edit: func(request *realtimev1.FallbackPlaybackRequest, _ *string) { request.TranslatedText = "" }},
		{name: "version missing", edit: func(request *realtimev1.FallbackPlaybackRequest, _ *string) { request.LanguageConfigVersion = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := valid
			sessionID := "session-1"
			tt.edit(&request, &sessionID)
			if _, err := client.PlayFallback(t.Context(), sessionID, request); !errors.Is(err, ErrClientRequest) {
				t.Fatalf("PlayFallback() error = %v, want ErrClientRequest", err)
			}
		})
	}

	ticketErr := errors.New("ticket unavailable")
	ticketClient, err := NewClient(ClientConfig{
		BaseURL: "https://realtime.example", HTTP: http.DefaultClient,
		Tickets: TicketSourceFunc(func(context.Context, string) (string, error) { return "", ticketErr }),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := ticketClient.PlayFallback(t.Context(), "session-1", valid); !errors.Is(err, ticketErr) {
		t.Fatalf("PlayFallback() ticket error = %v, want ticket error", err)
	}
}

func TestClientPlayFallbackRejectsInvalidReceipt(t *testing.T) {
	responses := []string{
		`{"operation_id":"other","status":"accepted"}`,
		`{"operation_id":"fallback-1","status":"unknown"}`,
		`not-json`,
	}
	for _, body := range responses {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusAccepted)
				_, _ = writer.Write([]byte(body))
			}))
			defer server.Close()
			_, err := newTestClient(t, server.URL).PlayFallback(t.Context(), "session-1", realtimev1.FallbackPlaybackRequest{
				OperationID: "fallback-1", SessionID: "session-1", TurnID: "turn-1", TargetLanguage: "zh-CN",
				TranslatedText: "translated", LanguageConfigVersion: 3, TraceID: "trace-1",
			})
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("PlayFallback() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestClientStopAllowsEmptyTraceID(t *testing.T) {
	endedAt := time.Unix(1700000060, 0).UTC()
	observations := make(chan stopRequestObservation, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body realtimev1.StopRequest
		decodeErr := json.NewDecoder(request.Body).Decode(&body)
		observations <- stopRequestObservation{
			authorization:  request.Header.Get("Authorization"),
			idempotencyKey: request.Header.Get("Idempotency-Key"),
			body:           body,
			err:            decodeErr,
		}
		_ = json.NewEncoder(writer).Encode(realtimev1.RuntimeSnapshot{
			SessionID: "session-1", RuntimeState: realtimev1.RuntimeStopped,
			UpdatedAt: endedAt,
		})
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server.URL)

	snapshot, err := client.Stop(t.Context(), "session-1", realtimev1.StopRequest{
		Reason:  "user_requested",
		EndedAt: endedAt,
	})
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	observation := <-observations
	if observation.err != nil {
		t.Fatalf("decode stop request: %v", observation.err)
	}
	if observation.authorization != "Bearer realtime-ticket" {
		t.Fatalf("Authorization = %q, want bearer ticket", observation.authorization)
	}
	if observation.idempotencyKey != "stop:user_requested" {
		t.Fatalf("Idempotency-Key = %q, want stop:user_requested", observation.idempotencyKey)
	}
	if observation.body.TraceID != "" ||
		observation.body.Reason != "user_requested" ||
		!observation.body.EndedAt.Equal(endedAt) {
		t.Fatalf("stop body = %#v", observation.body)
	}
	if snapshot.SessionID != "session-1" ||
		snapshot.RuntimeState != realtimev1.RuntimeStopped ||
		snapshot.UpdatedAt.IsZero() {
		t.Fatalf("Stop() = %#v", snapshot)
	}
}

type stopRequestObservation struct {
	authorization  string
	idempotencyKey string
	body           realtimev1.StopRequest
	err            error
}

func TestClientStopValidatesRequestAndResponse(t *testing.T) {
	client := newTestClient(t, "https://realtime.example")
	valid := realtimev1.StopRequest{
		Reason: "user_requested", EndedAt: time.Unix(1700000060, 0).UTC(),
	}
	tests := []struct {
		name      string
		sessionID string
		request   realtimev1.StopRequest
	}{
		{name: "empty session", sessionID: "", request: valid},
		{name: "empty reason", sessionID: "session-1", request: realtimev1.StopRequest{EndedAt: valid.EndedAt}},
		{name: "zero time", sessionID: "session-1", request: realtimev1.StopRequest{Reason: "user_requested"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := client.Stop(
				t.Context(), test.sessionID, test.request,
			); !errors.Is(err, ErrClientRequest) {
				t.Fatalf("Stop() error = %v, want ErrClientRequest", err)
			}
		})
	}

	fixture := newFixture(t)
	fixture.lifecycle.stopped.RuntimeState = realtimev1.RuntimePlaying
	server := httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)
	snapshot, err := newTestClient(t, server.URL).Stop(
		t.Context(),
		"session-1",
		realtimev1.StopRequest{
			TraceID: "trace-stop",
			Reason:  "user_requested",
			EndedAt: time.Unix(1700000060, 0).UTC(),
		},
	)
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if snapshot.RuntimeState != realtimev1.RuntimePlaying {
		t.Fatalf("Stop() state = %q, want pass-through valid state", snapshot.RuntimeState)
	}

	fixture = newFixture(t)
	fixture.lifecycle.stopped.RuntimeState = "unknown"
	server = httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)
	_, err = newTestClient(t, server.URL).Stop(
		t.Context(),
		"session-1",
		realtimev1.StopRequest{
			TraceID: "trace-stop",
			Reason:  "user_requested",
			EndedAt: time.Unix(1700000060, 0).UTC(),
		},
	)
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("Stop(invalid state) error = %v, want ErrInvalidResponse", err)
	}
}

func TestClientGetRuntimeStateMapsSnapshotsAndMissingRuntime(t *testing.T) {
	fixture := newFixture(t)
	server := httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)

	snapshot, err := newTestClient(t, server.URL).GetRuntimeState(
		t.Context(),
		"session-1",
	)
	if err != nil {
		t.Fatalf("GetRuntimeState() error = %v", err)
	}
	if snapshot.SessionID != "session-1" ||
		snapshot.RuntimeState != realtimev1.RuntimeListening ||
		snapshot.StartOperationID != "operation-1" ||
		snapshot.UpdatedAt.IsZero() {
		t.Fatalf("GetRuntimeState() = %#v", snapshot)
	}

	fixture = newFixture(t)
	fixture.lifecycle.runtimeErr = session.ErrRuntimeNotFound
	server = httptest.NewServer(fixture.handler)
	t.Cleanup(server.Close)
	_, err = newTestClient(t, server.URL).GetRuntimeState(t.Context(), "session-1")
	if !errors.Is(err, ErrRuntimeNotFound) {
		t.Fatalf("GetRuntimeState() error = %v, want ErrRuntimeNotFound", err)
	}
}

func TestClientGetConnectionMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "not found",
			err:  webrtc.ErrConnectionNotFound,
			want: ErrConnectionNotFound,
		},
		{
			name: "provider",
			err:  errors.New("provider unavailable"),
			want: ErrDependencyUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.connections.err = test.err
			server := httptest.NewServer(fixture.handler)
			t.Cleanup(server.Close)

			_, err := newTestClient(t, server.URL).GetConnection(
				t.Context(),
				"session-1",
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("GetConnection() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClientGetConnectionRejectsInvalidSnapshots(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	valid := realtimev1.ConnectionSnapshot{
		SessionID: "session-1", ConnectionID: "connection-1",
		State: realtimev1.ConnectionConnected, Version: 1, UpdatedAt: now,
	}
	tests := []struct {
		name string
		edit func(*realtimev1.ConnectionSnapshot)
	}{
		{name: "session mismatch", edit: func(value *realtimev1.ConnectionSnapshot) { value.SessionID = "session-2" }},
		{name: "empty connection", edit: func(value *realtimev1.ConnectionSnapshot) { value.ConnectionID = "" }},
		{name: "unknown state", edit: func(value *realtimev1.ConnectionSnapshot) { value.State = "unknown" }},
		{name: "invalid version", edit: func(value *realtimev1.ConnectionSnapshot) { value.Version = 0 }},
		{name: "zero timestamp", edit: func(value *realtimev1.ConnectionSnapshot) { value.UpdatedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.connections.snapshot = valid
			test.edit(&fixture.connections.snapshot)
			server := httptest.NewServer(fixture.handler)
			t.Cleanup(server.Close)

			_, err := newTestClient(t, server.URL).GetConnection(
				t.Context(),
				"session-1",
			)
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("GetConnection() error = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestClientGetConnectionRejectsEmptySession(t *testing.T) {
	client := newTestClient(t, "https://realtime.example")
	if _, err := client.GetConnection(t.Context(), ""); !errors.Is(err, ErrClientRequest) {
		t.Fatalf("GetConnection() error = %v, want ErrClientRequest", err)
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
			if _, err := client.GetConnection(ctx, "session-1"); !errors.Is(err, test.want) {
				t.Fatalf("GetConnection() error = %v, want %v", err, test.want)
			}
			if _, err := client.GetRuntimeState(ctx, "session-1"); !errors.Is(err, test.want) {
				t.Fatalf("GetRuntimeState() error = %v, want %v", err, test.want)
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
