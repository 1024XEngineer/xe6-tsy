package qwen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
)

func TestProviderRepliesThroughChatCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/compatible-mode/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("request = %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if len(body.Messages) != 2 || body.Messages[1].Content != "你好" || !strings.Contains(body.Messages[0].Content, "zh-CN") {
			t.Errorf("messages = %#v", body.Messages)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"你好，有什么可以帮你？"}}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`))
	}))
	defer server.Close()

	provider, err := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL + "/compatible-mode/v1"})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	result, err := provider.Reply(context.Background(), assistant.Request{Text: "你好", Language: "zh-CN"})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if result.Text != "你好，有什么可以帮你？" || result.Language != "zh-CN" || result.InputTokens != 5 || result.OutputTokens != 3 {
		t.Fatalf("result = %#v", result)
	}
}

func TestProviderRejectsInvalidConfigurationAndResponse(t *testing.T) {
	if _, err := NewProvider(Config{}); err != ErrAPIKeyRequired {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if _, err := NewProvider(Config{APIKey: "key"}); err != ErrEndpointRequired {
		t.Fatalf("NewProvider() error = %v", err)
	}
	provider, err := NewProvider(Config{APIKey: "key", BaseURL: "http://example.invalid"})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if _, err := provider.Reply(t.Context(), assistant.Request{}); err == nil {
		t.Fatal("Reply() error = nil")
	}
}

func TestSystemPromptUsesChineseAssistantName(t *testing.T) {
	prompt := systemPrompt("zh-CN")
	for _, want := range []string{
		"You are 小灵",
		"Lingow is the product brand",
		"always refer to yourself as 小灵",
		"never as Lingow",
		"zh-CN",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt missing %q: %s", want, prompt)
		}
	}
}
