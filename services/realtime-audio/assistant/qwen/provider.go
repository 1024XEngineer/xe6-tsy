// Package qwen adapts an OpenAI-compatible Qwen chat endpoint to assistant.Provider.
package qwen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/assistant"
)

const defaultModel = "qwen3.6-flash"

var (
	ErrAPIKeyRequired   = errors.New("Qwen assistant API key is required")
	ErrEndpointRequired = errors.New("Qwen assistant endpoint is required")
)

type Config struct {
	APIKey         string
	BaseURL        string
	Model          string
	Provider       string
	HTTPClient     *http.Client
	EnableThinking bool
	Timeout        time.Duration
}

type Provider struct {
	config Config
}

func NewProvider(config Config) (*Provider, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, ErrAPIKeyRequired
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		return nil, ErrEndpointRequired
	}
	if strings.TrimSpace(config.Model) == "" {
		config.Model = defaultModel
	}
	if config.Provider == "" {
		config.Provider = "aliyun"
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.Timeout <= 0 {
		config.Timeout = 15 * time.Second
	}
	return &Provider{config: config}, nil
}

func (p *Provider) Reply(ctx context.Context, request assistant.Request) (assistant.Result, error) {
	startedAt := time.Now()
	if err := ctx.Err(); err != nil {
		return assistant.Result{}, err
	}
	if strings.TrimSpace(request.Text) == "" {
		return assistant.Result{}, errors.New("assistant input text is required")
	}
	body := chatRequest{
		Model: p.config.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt(request.Language)},
			{Role: "user", Content: request.Text},
		},
		EnableThinking: p.config.EnableThinking,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return assistant.Result{}, fmt.Errorf("encode Qwen assistant request: %w", err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost,
		strings.TrimRight(p.config.BaseURL, "/")+"/chat/completions", strings.NewReader(string(encoded)))
	if err != nil {
		return assistant.Result{}, fmt.Errorf("create Qwen assistant request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := p.config.HTTPClient.Do(httpRequest)
	if err != nil {
		return assistant.Result{}, fmt.Errorf("call Qwen assistant: %w", err)
	}
	defer response.Body.Close()
	responseBytes, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return assistant.Result{}, fmt.Errorf("read Qwen assistant response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return assistant.Result{}, fmt.Errorf("Qwen assistant returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBytes)))
	}
	var decoded chatResponse
	if err := json.Unmarshal(responseBytes, &decoded); err != nil {
		return assistant.Result{}, fmt.Errorf("decode Qwen assistant response: %w", err)
	}
	if decoded.Error.Message != "" {
		return assistant.Result{}, fmt.Errorf("Qwen assistant failed: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 || strings.TrimSpace(decoded.Choices[0].Message.Content) == "" {
		return assistant.Result{}, errors.New("Qwen assistant returned no content")
	}
	return assistant.Result{
		Text: strings.TrimSpace(decoded.Choices[0].Message.Content), Language: request.Language,
		Provider: p.config.Provider, Model: p.config.Model,
		InputTokens: decoded.Usage.PromptTokens, OutputTokens: decoded.Usage.CompletionTokens,
		LatencyMS: time.Since(startedAt).Milliseconds(),
	}, nil
}

func systemPrompt(language string) string {
	return "You are 小灵, the Lingow voice assistant. Lingow is the product brand, while 小灵 is your Chinese spoken name. " +
		"When answering in Chinese or speaking with a Chinese user, always refer to yourself as 小灵, never as Lingow. " +
		"Answer the user's request directly in " + language + ". Keep the response concise and natural for speech output."
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Stream         bool          `json:"stream"`
	EnableThinking bool          `json:"enable_thinking"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

var _ assistant.Provider = (*Provider)(nil)
