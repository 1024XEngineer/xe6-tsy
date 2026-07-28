// Package qwen adapts Qwen3.6 Flash's OpenAI-compatible chat API to translation.
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

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
)

const defaultModel = "qwen3.6-flash"

var (
	ErrAPIKeyRequired   = errors.New("Qwen translation API key is required")
	ErrEndpointRequired = errors.New("Qwen translation endpoint is required")
	ErrModelRequired    = errors.New("Qwen translation model is required")
)

// Config contains the OpenAI-compatible endpoint and model settings.
type Config struct {
	APIKey         string
	BaseURL        string
	Model          string
	Provider       string
	HTTPClient     *http.Client
	EnableThinking bool
	Timeout        time.Duration
}

// Provider calls Qwen3.6 Flash for one final translation request.
type Provider struct {
	config Config
}

// NewProvider validates and normalizes a Qwen translation configuration.
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

func (p *Provider) Translate(ctx context.Context, request translate.Request) (translate.Result, error) {
	startedAt := time.Now()
	if err := ctx.Err(); err != nil {
		return translate.Result{}, err
	}
	if strings.TrimSpace(request.Text) == "" {
		return translate.Result{}, errors.New("translation text is required")
	}
	body := chatRequest{
		Model: p.config.Model,
		Messages: []chatMessage{
			{Role: "system", Content: fmt.Sprintf("Translate from %s to %s. Return only the translation without explanation.", request.SourceLanguage, request.TargetLanguage)},
			{Role: "user", Content: request.Text},
		},
		Stream:         false,
		EnableThinking: p.config.EnableThinking,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return translate.Result{}, fmt.Errorf("encode Qwen translation request: %w", err)
	}
	endpoint := strings.TrimRight(p.config.BaseURL, "/") + "/chat/completions"
	requestCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return translate.Result{}, fmt.Errorf("create Qwen translation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.config.HTTPClient.Do(req)
	if err != nil {
		return translate.Result{}, fmt.Errorf("call Qwen translation: %w", err)
	}
	defer resp.Body.Close()
	responseBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return translate.Result{}, fmt.Errorf("read Qwen translation response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return translate.Result{}, fmt.Errorf("Qwen translation returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBytes)))
	}
	var response chatResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return translate.Result{}, fmt.Errorf("decode Qwen translation response: %w", err)
	}
	if response.Error.Message != "" {
		return translate.Result{}, fmt.Errorf("Qwen translation failed: %s", response.Error.Message)
	}
	if len(response.Choices) == 0 || response.Choices[0].Message.Content == "" {
		return translate.Result{}, errors.New("Qwen translation returned no content")
	}
	return translate.Result{
		Text: response.Choices[0].Message.Content, Provider: p.config.Provider, Model: p.config.Model,
		InputTokens: response.Usage.PromptTokens, OutputTokens: response.Usage.CompletionTokens,
		LatencyMS: time.Since(startedAt).Milliseconds(),
	}, nil
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
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

var _ translate.Provider = (*Provider)(nil)
