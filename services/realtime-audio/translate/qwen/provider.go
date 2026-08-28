// Package qwen adapts Qwen3.6 Flash's OpenAI-compatible chat API to translation.
package qwen

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/rawlog"
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
	RawLogger      *rawlog.Logger
}

// Provider calls Qwen3.6 Flash for final and low-latency streamed translation requests.
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

	result, err := p.translateOnce(ctx, request, buildSystemPrompt(request.SourceLanguage, request.TargetLanguage))
	if err != nil {
		return translate.Result{}, err
	}
	if !translationLooksInvalid(result.Text, request.TargetLanguage) {
		result.LatencyMS = time.Since(startedAt).Milliseconds()
		return result, nil
	}

	// One reinforced retry keeps latency bounded while recovering from common
	// prompt-injection refusals that abandon the translation task.
	retried, err := p.translateOnce(ctx, request, buildReinforcedSystemPrompt(request.SourceLanguage, request.TargetLanguage))
	if err != nil {
		// Preserve first-attempt usage so pipeline can still publish consumption.
		return usageBearingResult(result, startedAt), err
	}
	retried.InputTokens += result.InputTokens
	retried.OutputTokens += result.OutputTokens
	if translationLooksInvalid(retried.Text, request.TargetLanguage) {
		slog.Warn("qwen translation rejected unexpected behavior",
			"session_id", request.SessionID,
			"turn_id", request.TurnID,
			"source_language", request.SourceLanguage,
			"target_language", request.TargetLanguage,
			"input_tokens", retried.InputTokens,
			"output_tokens", retried.OutputTokens,
		)
		return usageBearingResult(retried, startedAt), translate.ErrUnexpectedBehavior
	}
	retried.LatencyMS = time.Since(startedAt).Milliseconds()
	return retried, nil
}

// TranslateStream starts an OpenAI-compatible SSE completion. The callback is
// invoked for each non-empty model delta while the complete result is retained
// for the existing ordered subtitle/TTS boundary.
func (p *Provider) TranslateStream(ctx context.Context, request translate.Request, onDelta func(string)) (translate.Result, error) {
	startedAt := time.Now()
	if err := ctx.Err(); err != nil {
		return translate.Result{}, err
	}
	if strings.TrimSpace(request.Text) == "" {
		return translate.Result{}, errors.New("translation text is required")
	}
	result, err := p.translateStreamOnce(ctx, request, buildSystemPrompt(request.SourceLanguage, request.TargetLanguage), onDelta)
	if err != nil {
		return translate.Result{}, err
	}
	if translationLooksInvalid(result.Text, request.TargetLanguage) {
		// Keep the existing reinforced retry as a correctness fallback. It is
		// rare and only runs when the streamed answer is clearly not a translation.
		retried, retryErr := p.translateOnce(ctx, request, buildReinforcedSystemPrompt(request.SourceLanguage, request.TargetLanguage))
		if retryErr != nil {
			return usageBearingResult(result, startedAt), retryErr
		}
		retried.InputTokens += result.InputTokens
		retried.OutputTokens += result.OutputTokens
		if translationLooksInvalid(retried.Text, request.TargetLanguage) {
			return usageBearingResult(retried, startedAt), translate.ErrUnexpectedBehavior
		}
		retried.LatencyMS = time.Since(startedAt).Milliseconds()
		return retried, nil
	}
	result.LatencyMS = time.Since(startedAt).Milliseconds()
	return result, nil
}

func usageBearingResult(partial translate.Result, startedAt time.Time) translate.Result {
	partial.Text = ""
	partial.LatencyMS = time.Since(startedAt).Milliseconds()
	return partial
}

func (p *Provider) translateOnce(ctx context.Context, request translate.Request, systemPrompt string) (translate.Result, error) {
	body := chatRequest{
		Model: p.config.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildUserContent(request.Text, request.SourceLanguage, request.TargetLanguage)},
		},
		Stream:         false,
		EnableThinking: p.config.EnableThinking,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return translate.Result{}, fmt.Errorf("encode Qwen translation request: %w", err)
	}
	_ = p.config.RawLogger.WriteJSON(request.SessionID, "llm", "request", "chat.completions", encoded)
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
	_ = p.config.RawLogger.WriteJSON(request.SessionID, "llm", "response", "chat.completions", responseBytes)
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
		Text:         response.Choices[0].Message.Content,
		Provider:     p.config.Provider,
		Model:        p.config.Model,
		InputTokens:  response.Usage.PromptTokens,
		OutputTokens: response.Usage.CompletionTokens,
	}, nil
}

func (p *Provider) translateStreamOnce(ctx context.Context, request translate.Request, systemPrompt string, onDelta func(string)) (translate.Result, error) {
	body := chatRequest{
		Model: p.config.Model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: buildUserContent(request.Text, request.SourceLanguage, request.TargetLanguage)},
		},
		Stream: true, StreamOptions: &streamOptions{IncludeUsage: true}, EnableThinking: p.config.EnableThinking,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return translate.Result{}, fmt.Errorf("encode Qwen streaming translation request: %w", err)
	}
	_ = p.config.RawLogger.WriteJSON(request.SessionID, "llm", "request", "chat.completions.stream", encoded)
	endpoint := strings.TrimRight(p.config.BaseURL, "/") + "/chat/completions"
	requestCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return translate.Result{}, fmt.Errorf("create Qwen streaming translation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.config.HTTPClient.Do(req)
	if err != nil {
		return translate.Result{}, fmt.Errorf("call Qwen streaming translation: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		return translate.Result{}, fmt.Errorf("Qwen streaming translation returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBytes)))
	}

	type streamChunk struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	var text strings.Builder
	var usageIn, usageOut int64
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		_ = p.config.RawLogger.WriteJSON(request.SessionID, "llm", "response", "chat.completions.stream.chunk", []byte(payload))
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return translate.Result{}, fmt.Errorf("decode Qwen streaming translation chunk: %w", err)
		}
		if chunk.Error.Message != "" {
			return translate.Result{}, fmt.Errorf("Qwen streaming translation failed: %s", chunk.Error.Message)
		}
		usageIn += chunk.Usage.PromptTokens
		usageOut += chunk.Usage.CompletionTokens
		if len(chunk.Choices) == 0 || chunk.Choices[0].Delta.Content == "" {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		text.WriteString(delta)
		if onDelta != nil {
			onDelta(delta)
		}
	}
	if err := scanner.Err(); err != nil {
		return translate.Result{}, fmt.Errorf("read Qwen streaming translation: %w", err)
	}
	if strings.TrimSpace(text.String()) == "" {
		return translate.Result{}, errors.New("Qwen streaming translation returned no content")
	}
	return translate.Result{Text: text.String(), Provider: p.config.Provider, Model: p.config.Model,
		InputTokens: usageIn, OutputTokens: usageOut}, nil
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	Stream         bool           `json:"stream"`
	StreamOptions  *streamOptions `json:"stream_options,omitempty"`
	EnableThinking bool           `json:"enable_thinking"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
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
