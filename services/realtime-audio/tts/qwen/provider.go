// Package qwen adapts Qwen3-TTS-Flash's streaming HTTP API to the local TTS port.
package qwen

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

const defaultModel = "qwen3-tts-flash"

var (
	ErrAPIKeyRequired   = errors.New("Qwen TTS API key is required")
	ErrEndpointRequired = errors.New("Qwen TTS endpoint is required")
	ErrModelRequired    = errors.New("Qwen TTS model is required")
)

// Config contains Qwen TTS HTTP and audio settings.
type Config struct {
	APIKey     string
	BaseURL    string
	Model      string
	Provider   string
	Voice      string
	SampleRate int
	HTTPClient *http.Client
	Timeout    time.Duration
}

// Provider starts Qwen TTS streaming requests.
type Provider struct {
	config Config
}

// NewProvider validates and normalizes a Qwen TTS configuration.
func NewProvider(config Config) (*Provider, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, ErrAPIKeyRequired
	}
	if strings.TrimSpace(config.BaseURL) == "" {
		return nil, ErrEndpointRequired
	}
	if config.Model == "" {
		config.Model = defaultModel
	}
	if config.Provider == "" {
		config.Provider = "aliyun"
	}
	if config.Voice == "" {
		config.Voice = "Cherry"
	}
	if config.SampleRate <= 0 {
		config.SampleRate = 24000
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	return &Provider{config: config}, nil
}

func (p *Provider) StartStream(ctx context.Context, request tts.Request) (tts.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Text) == "" {
		return nil, errors.New("TTS text is required")
	}
	streamCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	s := &stream{
		ctx: streamCtx, cancel: cancel, config: p.config,
		request: request, chunks: make(chan tts.AudioChunk, 16), done: make(chan struct{}),
	}
	go s.run()
	return s, nil
}

type stream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	config  Config
	request tts.Request
	chunks  chan tts.AudioChunk
	done    chan struct{}

	stateMu   sync.Mutex
	result    tts.Result
	err       error
	closeOnce sync.Once
}

func (s *stream) Chunks() <-chan tts.AudioChunk { return s.chunks }

func (s *stream) Finish(ctx context.Context) (tts.Result, error) {
	select {
	case <-s.done:
		s.stateMu.Lock()
		defer s.stateMu.Unlock()
		return s.result, s.err
	case <-ctx.Done():
		s.shutdown()
		return tts.Result{}, ctx.Err()
	}
}

func (s *stream) Close() error {
	s.shutdown()
	return nil
}

func (s *stream) run() {
	defer func() {
		if err := s.ctx.Err(); err != nil {
			s.setError(err)
		}
		s.shutdown()
	}()
	requestBody := generationRequest{
		Model: s.config.Model,
		Input: generationInput{Text: s.request.Text, Voice: firstNonEmpty(s.request.VoiceID, s.config.Voice), LanguageType: languageType(s.request.TargetLanguage)},
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		s.setError(fmt.Errorf("encode Qwen TTS request: %w", err))
		return
	}
	endpoint := ttsEndpoint(s.config.BaseURL)
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		s.setError(fmt.Errorf("create Qwen TTS request: %w", err))
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DashScope-SSE", "enable")
	resp, err := s.config.HTTPClient.Do(req)
	if err != nil {
		if s.ctx.Err() == nil {
			s.setError(fmt.Errorf("call Qwen TTS: %w", err))
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		s.setError(fmt.Errorf("Qwen TTS returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
		return
	}
	var sequence int64
	var totalBytes int64
	var audioURL string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") || strings.HasPrefix(line, ":") {
			continue
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var chunk generationResponse
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			s.setError(fmt.Errorf("decode Qwen TTS event: %w", err))
			return
		}
		if chunk.Code != "" || chunk.Message != "" {
			s.setError(fmt.Errorf("Qwen TTS failed: %s", firstNonEmpty(chunk.Message, chunk.Code)))
			return
		}
		audioURL = firstNonEmpty(audioURL, chunk.Output.Audio.URL)
		if chunk.Output.Audio.Data == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(chunk.Output.Audio.Data)
		if err != nil {
			s.setError(fmt.Errorf("decode Qwen TTS audio: %w", err))
			return
		}
		if len(data) == 0 {
			continue
		}
		sequence++
		totalBytes += int64(len(data))
		select {
		case s.chunks <- tts.AudioChunk{SequenceNo: sequence, Data: data}:
		case <-s.ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil && s.ctx.Err() == nil {
		s.setError(fmt.Errorf("read Qwen TTS stream: %w", err))
		return
	}
	if sequence == 0 && audioURL != "" {
		data, err := s.downloadAudio(audioURL)
		if err != nil {
			s.setError(err)
			return
		}
		sequence = 1
		totalBytes = int64(len(data))
		select {
		case s.chunks <- tts.AudioChunk{SequenceNo: sequence, Data: data}:
		case <-s.ctx.Done():
			return
		}
	}
	s.stateMu.Lock()
	s.result = tts.Result{Provider: s.config.Provider, Model: s.config.Model, AudioDuration: pcmDuration(totalBytes, s.config.SampleRate)}
	s.stateMu.Unlock()
}

func (s *stream) downloadAudio(rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create Qwen TTS audio request: %w", err)
	}
	resp, err := s.config.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download Qwen TTS audio: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("download Qwen TTS audio returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("read Qwen TTS audio: %w", err)
	}
	return data, nil
}

func (s *stream) setError(err error) {
	s.stateMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.stateMu.Unlock()
}

func (s *stream) shutdown() {
	s.closeOnce.Do(func() {
		s.cancel()
		close(s.chunks)
		close(s.done)
	})
}

func ttsEndpoint(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/services/aigc/multimodal-generation/generation") {
		return base
	}
	return base + "/services/aigc/multimodal-generation/generation"
}

func languageType(language string) string {
	if strings.HasPrefix(strings.ToLower(language), "zh") {
		return "Chinese"
	}
	if strings.HasPrefix(strings.ToLower(language), "en") {
		return "English"
	}
	return language
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func pcmDuration(bytes int64, sampleRate int) time.Duration {
	if bytes <= 0 || sampleRate <= 0 {
		return 0
	}
	return time.Duration(bytes) * time.Second / time.Duration(sampleRate*2)
}

type generationRequest struct {
	Model string          `json:"model"`
	Input generationInput `json:"input"`
}

type generationInput struct {
	Text         string `json:"text"`
	Voice        string `json:"voice"`
	LanguageType string `json:"language_type,omitempty"`
}

type generationResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Output  struct {
		Audio struct {
			Data string `json:"data"`
			URL  string `json:"url"`
		} `json:"audio"`
	} `json:"output"`
}

var _ tts.Provider = (*Provider)(nil)
var _ tts.Stream = (*stream)(nil)
