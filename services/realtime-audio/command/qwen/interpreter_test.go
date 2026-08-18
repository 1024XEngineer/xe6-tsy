package qwen

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
)

func TestInterpreterProducesUntrustedCandidateFromStrictJSON(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/compatible-mode/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("request = %s auth %q", request.URL.Path, request.Header.Get("Authorization"))
		}
		var body chatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.ResponseFormat.Type != "json_object" || body.EnableThinking || len(body.Messages) != 2 || body.Messages[1].Content != "帮我进入中文翻译成英文的同传状态" {
			t.Errorf("body = %#v", body)
		}
		if !strings.Contains(body.Messages[0].Content, `"mode":"interpretation"`) || strings.Contains(body.Messages[0].Content, "english_practice") {
			t.Errorf("system prompt capability surface = %q", body.Messages[0].Content)
		}
		if !strings.Contains(body.Messages[0].Content, "Chinese zh-CN") || !strings.Contains(body.Messages[0].Content, "Japanese ja-JP") {
			t.Errorf("system prompt language guidance = %q", body.Messages[0].Content)
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-command","object":"chat.completion","created":1760000000,"model":"qwen3.6-flash","system_fingerprint":{},"choices":[{"index":0,"message":{"role":"assistant","content":"{\"action\":\"activate_mode\",\"target_mode\":\"interpretation\",\"arguments\":{\"source_language\":\"zh-CN\",\"target_language\":\"en-US\"}}"},"finish_reason":"stop","logprobs":null}],"usage":{"prompt_tokens":42,"completion_tokens":12,"total_tokens":54,"prompt_tokens_details":{"text_tokens":42},"completion_tokens_details":{"text_tokens":12}}}`))
	}))
	defer server.Close()
	interpreter := newTestInterpreter(t, Config{APIKey: "test-key", BaseURL: server.URL + "/compatible-mode/v1", HTTPClient: server.Client()})
	candidate, err := interpreter.Interpret(t.Context(), validRequest())
	if err != nil {
		t.Fatalf("Interpret() error = %v", err)
	}
	if candidate.Action != command.ActionActivateMode || candidate.TargetMode != realtimev1.ModeInterpretation ||
		candidate.Arguments.SourceLanguage != "zh-CN" || candidate.Arguments.TargetLanguage != "en-US" {
		t.Fatalf("candidate = %#v", candidate)
	}
}

func TestInterpreterPreservesOrdinaryQuestionForAssistantQuery(t *testing.T) {
	t.Parallel()
	const question = "帮我查一下今天上海的天气"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body chatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(body.Messages) != 2 || body.Messages[1].Content != question ||
			!strings.Contains(body.Messages[0].Content, string(command.ActionAssistantQuery)) {
			t.Errorf("assistant query request = %#v", body)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"action\":\"assistant_query\",\"target_mode\":\"assistant\",\"arguments\":{}}"}}]}`))
	}))
	defer server.Close()
	interpreter := newTestInterpreter(t, Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	candidate, err := interpreter.Interpret(t.Context(), command.InterpretRequest{
		SessionID: "session-1", CommandID: "command-1", Text: question, Language: "zh-CN",
	})
	if err != nil {
		t.Fatalf("Interpret() error = %v", err)
	}
	if candidate.Text != question || candidate.Action != command.ActionAssistantQuery ||
		candidate.TargetMode != realtimev1.ModeAssistant || candidate.Arguments != (command.Arguments{}) {
		t.Fatalf("assistant candidate = %#v", candidate)
	}
}

func TestInterpreterDropsSpuriousLanguageArgumentsFromAssistantQuery(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"action\":\"assistant_query\",\"target_mode\":\"assistant\",\"arguments\":{\"source_language\":\"zh-CN\",\"target_language\":\"zh-CN\"}}"}}]}`))
	}))
	defer server.Close()
	interpreter := newTestInterpreter(t, Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	candidate, err := interpreter.Interpret(t.Context(), command.InterpretRequest{
		SessionID: "session-1", CommandID: "command-1", Text: "今天的天气怎么样", Language: "zh-CN",
	})
	if err != nil {
		t.Fatalf("Interpret() error = %v", err)
	}
	if candidate.Action != command.ActionAssistantQuery || candidate.TargetMode != realtimev1.ModeAssistant ||
		candidate.Arguments != (command.Arguments{}) {
		t.Fatalf("assistant candidate = %#v", candidate)
	}
}

func TestInterpreterGeneratesSuccessFeedbackFromAuthoritativeFacts(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body chatRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[0].Content, "immutable execution facts") {
			t.Errorf("feedback prompt = %#v", body.Messages)
		}
		var facts feedbackFacts
		if err := json.Unmarshal([]byte(body.Messages[1].Content), &facts); err != nil {
			t.Errorf("decode feedback facts: %v", err)
		}
		if facts.UserCommand != "切换为中日传译" || facts.ResponseLanguage != "zh-CN" ||
			facts.ModeSwitchStatus != realtimev1.ModeSwitchUnchanged || facts.LanguageConfig == nil ||
			facts.LanguageConfig.SourceLanguage != "zh-CN" || facts.LanguageConfig.TargetLanguage != "ja-JP" ||
			facts.LanguageConfig.Version != 3 {
			t.Errorf("feedback facts = %#v", facts)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"message\":\"好的，已切换为中文和日语同声传译。\"}"}}]}`))
	}))
	defer server.Close()
	interpreter := newTestInterpreter(t, Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})

	message, err := interpreter.GenerateSuccessFeedback(t.Context(), command.SuccessFeedbackRequest{
		Command: command.Command{
			Text: "切换为中日传译", Action: command.ActionActivateMode, TargetMode: realtimev1.ModeInterpretation,
		},
		Execution: command.ExecutionResult{
			Status: realtimev1.ModeSwitchUnchanged,
			State:  realtimev1.ModeStateSnapshot{ActiveMode: realtimev1.ModeInterpretation},
			LanguageConfig: &command.AppliedLanguageConfig{
				SourceLanguage: "zh-CN", TargetLanguage: "ja-JP", Version: 3,
			},
		},
		ResponseLanguage: "zh-CN",
	})
	if err != nil {
		t.Fatalf("GenerateSuccessFeedback() error = %v", err)
	}
	if message != "好的，已切换为中文和日语同声传译。" {
		t.Fatalf("feedback message = %q", message)
	}
}

func TestInterpreterRejectsInvalidSuccessFeedback(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"message\":\"第一行\\n第二行\"}"}}]}`))
	}))
	defer server.Close()
	interpreter := newTestInterpreter(t, Config{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	_, err := interpreter.GenerateSuccessFeedback(t.Context(), command.SuccessFeedbackRequest{
		Command: command.Command{Action: command.ActionReturnToAssistant, TargetMode: realtimev1.ModeAssistant},
		Execution: command.ExecutionResult{
			Status: realtimev1.ModeSwitchApplied,
			State:  realtimev1.ModeStateSnapshot{ActiveMode: realtimev1.ModeAssistant},
		},
	})
	if !errors.Is(err, ErrFeedbackInvalid) {
		t.Fatalf("GenerateSuccessFeedback() error = %v, want ErrFeedbackInvalid", err)
	}
}

func TestInterpreterRejectsMalformedOrExpandedProviderOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
	}{
		{name: "markdown", content: "```json\n{}\n```"},
		{name: "trailing JSON", content: `{}` + ` {}`},
		{name: "unknown field", content: `{"action":"activate_mode","target_mode":"interpretation","arguments":{},"tool":"switch"}`},
		{name: "unknown argument", content: `{"action":"activate_mode","target_mode":"interpretation","arguments":{"level":"advanced"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": test.content}}}})
			}))
			defer server.Close()
			interpreter := newTestInterpreter(t, Config{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client()})
			if _, err := interpreter.Interpret(t.Context(), validRequest()); !errors.Is(err, ErrResponseInvalid) {
				t.Fatalf("Interpret() error = %v, want ErrResponseInvalid", err)
			}
		})
	}
}

func TestInterpreterRejectsEnvelopeExpansionAndOversize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		response string
		limit    int64
		wantErr  error
	}{
		{name: "unknown envelope field", response: `{"choices":[],"unexpected":true}`, wantErr: ErrResponseInvalid},
		{name: "trailing envelope", response: `{"choices":[]} {}`, wantErr: ErrResponseInvalid},
		{name: "oversize", response: strings.Repeat("x", 65), limit: 64, wantErr: ErrResponseTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			interpreter := newTestInterpreter(t, Config{APIKey: "key", BaseURL: server.URL, HTTPClient: server.Client(), MaxResponse: test.limit})
			if _, err := interpreter.Interpret(t.Context(), validRequest()); !errors.Is(err, test.wantErr) {
				t.Fatalf("Interpret() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestInterpreterHonorsTimeoutAndCancellation(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	interpreter := newTestInterpreter(t, Config{APIKey: "key", BaseURL: "http://command.test", HTTPClient: client, Timeout: 20 * time.Millisecond})
	if _, err := interpreter.Interpret(context.Background(), validRequest()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Interpret(timeout) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := interpreter.Interpret(ctx, validRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Interpret(cancel) error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNewInterpreterValidatesConfiguration(t *testing.T) {
	t.Parallel()
	if _, err := NewInterpreter(Config{}); !errors.Is(err, ErrAPIKeyRequired) {
		t.Fatalf("NewInterpreter() error = %v", err)
	}
	if _, err := NewInterpreter(Config{APIKey: "key"}); !errors.Is(err, ErrEndpointRequired) {
		t.Fatalf("NewInterpreter() error = %v", err)
	}
	if _, err := NewInterpreter(Config{APIKey: "key", BaseURL: "http://example.invalid"}); !errors.Is(err, ErrCapabilitiesNeeded) {
		t.Fatalf("NewInterpreter() error = %v", err)
	}
}

func newTestInterpreter(t *testing.T, config Config) *Interpreter {
	t.Helper()
	config.Capabilities = []command.CapabilityDescriptor{
		{Mode: realtimev1.ModeAssistant, Description: "通用助手", SchemaVersion: 1, Actions: []command.Action{command.ActionReturnToAssistant, command.ActionAssistantQuery}},
		{Mode: realtimev1.ModeInterpretation, Description: "双语同传", SchemaVersion: 1, Actions: []command.Action{command.ActionActivateMode}},
	}
	interpreter, err := NewInterpreter(config)
	if err != nil {
		t.Fatalf("NewInterpreter() error = %v", err)
	}
	return interpreter
}

func validRequest() command.InterpretRequest {
	return command.InterpretRequest{
		SessionID: "session-1", CommandID: "command-1", Text: "帮我进入中文翻译成英文的同传状态", Language: "zh-CN",
	}
}
