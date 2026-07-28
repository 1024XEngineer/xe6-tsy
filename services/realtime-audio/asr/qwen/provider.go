// Package qwen adapts Qwen ASR realtime WebSocket events to the local ASR port.
package qwen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/gorilla/websocket"
)

const defaultModel = "qwen3-asr-flash-realtime"

var (
	ErrAPIKeyRequired   = errors.New("Qwen ASR API key is required")
	ErrEndpointRequired = errors.New("Qwen ASR WebSocket endpoint is required")
	ErrModelRequired    = errors.New("Qwen ASR model is required")
)

// Config contains only provider-specific transport and session settings.
type Config struct {
	APIKey          string
	BaseURL         string
	WebSocketURL    string
	Model           string
	Provider        string
	SampleRate      int
	VADThreshold    float64
	SilenceDuration time.Duration
	Dialer          *websocket.Dialer
}

// Provider starts Qwen realtime ASR streams.
type Provider struct {
	config Config
}

// NewProvider validates and normalizes a Qwen ASR configuration.
func NewProvider(config Config) (*Provider, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, ErrAPIKeyRequired
	}
	if strings.TrimSpace(config.WebSocketURL) == "" {
		config.WebSocketURL = deriveWebSocketURL(config.BaseURL)
	}
	if config.WebSocketURL == "" {
		return nil, ErrEndpointRequired
	}
	if config.Model == "" {
		config.Model = defaultModel
	}
	if config.SampleRate <= 0 {
		config.SampleRate = 16000
	}
	if config.SilenceDuration <= 0 {
		config.SilenceDuration = 500 * time.Millisecond
	}
	if config.Provider == "" {
		config.Provider = "aliyun"
	}
	if config.Dialer == nil {
		config.Dialer = websocket.DefaultDialer
	}
	return &Provider{config: config}, nil
}

func (p *Provider) StartStream(ctx context.Context, request asr.StreamRequest) (asr.Stream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint, err := realtimeEndpoint(p.config.WebSocketURL, p.config.Model)
	if err != nil {
		return nil, err
	}
	headers := http.Header{"Authorization": []string{"Bearer " + p.config.APIKey}}
	conn, _, err := p.config.Dialer.DialContext(ctx, endpoint, headers)
	if err != nil {
		return nil, fmt.Errorf("connect Qwen ASR: %w", err)
	}
	streamCtx, cancel := context.WithCancel(ctx)
	s := &stream{
		conn: conn, cancel: cancel, model: p.config.Model, provider: p.config.Provider,
		sampleRate: p.config.SampleRate, events: make(chan asr.Event, 64), done: make(chan struct{}), readDone: make(chan struct{}),
	}
	if err := s.write(streamCtx, sessionUpdateEvent(request.SourceLanguage, p.config)); err != nil {
		cancel()
		_ = conn.Close()
		return nil, fmt.Errorf("configure Qwen ASR session: %w", err)
	}
	go s.readLoop(streamCtx)
	return s, nil
}

func sessionUpdateEvent(language string, config Config) map[string]any {
	transcription := map[string]any{}
	if language != "" {
		transcription["language"] = languageCode(language)
	}
	return map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"input_audio_format":        "pcm",
			"sample_rate":               config.SampleRate,
			"input_audio_transcription": transcription,
			"turn_detection": map[string]any{
				"type":                "server_vad",
				"threshold":           config.VADThreshold,
				"silence_duration_ms": config.SilenceDuration.Milliseconds(),
			},
		},
	}
}

func languageCode(language string) string {
	if index := strings.IndexByte(language, '-'); index > 0 {
		return language[:index]
	}
	if index := strings.IndexByte(language, '_'); index > 0 {
		return language[:index]
	}
	return language
}

func deriveWebSocketURL(base string) string {
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	u.Scheme = "wss"
	path := strings.TrimSuffix(u.Path, "/")
	for _, suffix := range []string{"/compatible-mode/v1", "/api/v1"} {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
			break
		}
	}
	u.Path = path + "/api-ws/v1/realtime"
	u.RawQuery = ""
	return u.String()
}

func realtimeEndpoint(raw, model string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", ErrEndpointRequired
	}
	query := u.Query()
	query.Set("model", model)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

type stream struct {
	conn       *websocket.Conn
	cancel     context.CancelFunc
	model      string
	provider   string
	sampleRate int
	events     chan asr.Event
	done       chan struct{}
	readDone   chan struct{}

	writeMu sync.Mutex
	stateMu sync.Mutex
	result  asr.FinalResult
	err     error
	started int64
	ended   int64
	finish  sync.Once
	stop    sync.Once
	closed  sync.Once
}

func (s *stream) PushAudio(ctx context.Context, audio []byte) error {
	if len(audio) == 0 {
		return nil
	}
	return s.write(ctx, map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(audio),
	})
}

func (s *stream) Events() <-chan asr.Event { return s.events }

func (s *stream) Finish(ctx context.Context) (asr.FinalResult, error) {
	s.finish.Do(func() {
		if err := s.write(ctx, map[string]any{"type": "session.finish"}); err != nil {
			s.setError(err)
			s.shutdown()
		}
	})
	select {
	case <-s.done:
		return s.finalResult()
	case <-ctx.Done():
		s.shutdown()
		return asr.FinalResult{}, ctx.Err()
	}
}

func (s *stream) Close() error {
	s.shutdown()
	return nil
}

func (s *stream) write(ctx context.Context, value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return err
	}
	return nil
}

func (s *stream) readLoop(ctx context.Context) {
	defer func() {
		close(s.readDone)
		s.closeEvents()
	}()
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				s.setError(fmt.Errorf("read Qwen ASR event: %w", err))
			}
			return
		}
		if err := s.handleEvent(data); err != nil {
			s.setError(err)
			return
		}
		if s.isFinished(data) {
			return
		}
	}
}

func (s *stream) handleEvent(data []byte) error {
	var event serverEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("decode Qwen ASR event: %w", err)
	}
	switch event.Type {
	case "input_audio_buffer.speech_started":
		s.started = event.AudioStartMS
	case "input_audio_buffer.speech_stopped":
		s.ended = event.AudioEndMS
	case "conversation.item.input_audio_transcription.text":
		text := event.Text
		if text == "" {
			text = event.Stash
		}
		if text != "" {
			s.events <- asr.Event{Type: asr.EventPartial, Text: text}
		}
	case "conversation.item.input_audio_transcription.completed":
		result := asr.FinalResult{
			Text: event.Transcript, SourceLanguage: event.Language,
			Provider: s.provider, Model: s.model,
			AudioStart: time.Duration(s.started) * time.Millisecond,
			AudioEnd:   time.Duration(s.ended) * time.Millisecond,
		}
		if result.AudioEnd > result.AudioStart {
			result.AudioDuration = result.AudioEnd - result.AudioStart
		}
		s.stateMu.Lock()
		s.result = result
		s.stateMu.Unlock()
		s.events <- asr.Event{Type: asr.EventFinal, Text: result.Text, Final: &result}
	case "conversation.item.input_audio_transcription.failed", "error":
		return fmt.Errorf("Qwen ASR event failed: %s", event.Error.Message)
	}
	return nil
}

func (s *stream) isFinished(data []byte) bool {
	var event struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(data, &event) == nil && event.Type == "session.finished"
}

func (s *stream) setError(err error) {
	s.stateMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.stateMu.Unlock()
}

func (s *stream) finalResult() (asr.FinalResult, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.result, s.err
}

func (s *stream) shutdown() {
	s.stop.Do(func() {
		s.cancel()
		_ = s.conn.Close()
	})
	<-s.readDone
	s.closeEvents()
}

func (s *stream) closeEvents() {
	s.closed.Do(func() {
		close(s.events)
		close(s.done)
	})
}

type serverEvent struct {
	Type         string `json:"type"`
	AudioStartMS int64  `json:"audio_start_ms"`
	AudioEndMS   int64  `json:"audio_end_ms"`
	Language     string `json:"language"`
	Text         string `json:"text"`
	Stash        string `json:"stash"`
	Transcript   string `json:"transcript"`
	Error        struct {
		Message string `json:"message"`
	} `json:"error"`
}

var _ asr.Provider = (*Provider)(nil)
var _ asr.Stream = (*stream)(nil)
