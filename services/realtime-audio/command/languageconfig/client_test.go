package languageconfig

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
)

const testSystemToken = "command-system-token-secret-123456"

func TestClientConfigure(t *testing.T) {
	request := testRequest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/base/internal/v1/voice-sessions/session-1/language-config" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get(systemTokenHeader) != testSystemToken || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers = %#v", r.Header)
		}
		_, _ = io.WriteString(w, `{"session_id":"session-1","command_id":"command-1","source_language":"zh-CN","target_language":"en-US","output_mode":"single","version":2}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL + "/base", SystemToken: testSystemToken})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Configure(t.Context(), request)
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if result.SessionID != request.SessionID || result.CommandID != request.CommandID || result.Version != 2 {
		t.Fatalf("result = %#v", result)
	}
}

func TestClientConfigureRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "identity mismatch", body: `{"session_id":"other","command_id":"command-1","version":1}`, want: ErrResponseInvalid},
		{name: "zero version", body: `{"session_id":"session-1","command_id":"command-1","version":0}`, want: ErrResponseInvalid},
		{name: "unknown field", body: `{"session_id":"session-1","command_id":"command-1","version":1,"extra":true}`, want: ErrResponseInvalid},
		{name: "trailing JSON", body: `{"session_id":"session-1","command_id":"command-1","version":1}{}`, want: ErrResponseInvalid},
		{name: "oversized", body: strings.Repeat("x", int(maxResponseBytes)+1), want: ErrResponseTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tt.body) }))
			defer server.Close()
			client, err := NewClient(Config{BaseURL: server.URL, SystemToken: testSystemToken})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			_, err = client.Configure(t.Context(), testRequest())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Configure() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestClientConfigureReturnsTypedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"code":"idempotency_conflict"}}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, SystemToken: testSystemToken})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Configure(t.Context(), testRequest())
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusConflict || httpErr.Code != "idempotency_conflict" {
		t.Fatalf("Configure() error = %#v", err)
	}
}

func TestClientConfigureMapsMissingDeliveryTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":{"code":"delivery_target_required"}}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, SystemToken: testSystemToken})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Configure(t.Context(), testRequest())
	if !errors.Is(err, command.ErrDeliveryTargetRequired) {
		t.Fatalf("Configure() error = %v, want delivery target error", err)
	}
}

func TestClientConfigureFallsBackToLegacyAPIForBidirectionalMode(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request %d: %v", requests, err)
		}
		if requests == 1 {
			if body["output_mode"] != "bidirectional" || body["expected_version"] != float64(3) {
				t.Errorf("current request = %#v", body)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"code":"invalid_request","message":"unknown field","details":{}}}`)
			return
		}
		if _, ok := body["output_mode"]; ok {
			t.Errorf("legacy request contains output_mode: %#v", body)
		}
		if _, ok := body["expected_version"]; ok {
			t.Errorf("legacy request contains expected_version: %#v", body)
		}
		_, _ = io.WriteString(w, `{"session_id":"session-1","command_id":"command-1","version":4}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, SystemToken: testSystemToken})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	expectedVersion := 3
	request := testRequest()
	request.OutputMode = languagesv1.InterpretationOutputModeBidirectional
	request.ExpectedVersion = &expectedVersion
	result, err := client.Configure(t.Context(), request)
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if requests != 2 || result.OutputMode != languagesv1.InterpretationOutputModeBidirectional || result.Version != 4 {
		t.Fatalf("requests=%d result=%#v", requests, result)
	}
}

func TestClientConfigureDoesNotFallbackSingleOutputToLegacyAPI(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_request"}}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, SystemToken: testSystemToken})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Configure(t.Context(), testRequest())
	var httpErr *HTTPError
	if requests != 1 || !errors.As(err, &httpErr) || httpErr.Code != "invalid_request" {
		t.Fatalf("requests=%d error=%v", requests, err)
	}
}

func TestClientConfigurePreservesCancellationAndTimeout(t *testing.T) {
	tests := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		do      HTTPDoer
		timeout time.Duration
		want    error
	}{
		{
			name: "caller cancellation",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			do:   HTTPDoerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("must not call") }),
			want: context.Canceled,
		},
		{
			name: "client timeout",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			do: HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			}),
			timeout: time.Nanosecond,
			want:    context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(Config{BaseURL: "http://api.invalid", SystemToken: testSystemToken, HTTP: tt.do, Timeout: tt.timeout})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			ctx, cancel := tt.context()
			defer cancel()
			_, err = client.Configure(ctx, testRequest())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Configure() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewClientRejectsInvalidConfiguration(t *testing.T) {
	for _, config := range []Config{
		{},
		{BaseURL: "api:8080", SystemToken: testSystemToken},
		{BaseURL: "http://api:8080", SystemToken: "short"},
	} {
		if _, err := NewClient(config); !errors.Is(err, ErrConfigurationInvalid) {
			t.Fatalf("NewClient(%#v) error = %v", config, err)
		}
	}
}

func testRequest() languagesv1.CommandConfigRequest {
	return languagesv1.CommandConfigRequest{
		SessionID: "session-1", CommandID: "command-1", SourceLanguage: "zh-CN", TargetLanguage: "en-US",
		OutputMode: languagesv1.InterpretationOutputModeSingle,
	}
}

type HTTPDoerFunc func(*http.Request) (*http.Response, error)

func (f HTTPDoerFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }
