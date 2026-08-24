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
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/rawlog"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
	"github.com/gorilla/websocket"
)

const defaultModel = "qwen3-tts-flash"
const realtimeModel = "qwen3-tts-flash-realtime"
const realtimeSampleRate = 24000

var (
	ErrAPIKeyRequired     = errors.New("Qwen TTS API key is required")
	ErrEndpointRequired   = errors.New("Qwen TTS endpoint is required")
	ErrModelRequired      = errors.New("Qwen TTS model is required")
	ErrNoAudio            = errors.New("Qwen TTS returned no audio")
	ErrAudioURLNotAllowed = errors.New("Qwen TTS audio URL is not allowed")
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
	// AudioURLAllowlist contains exact hostnames accepted for URL-only audio responses.
	AudioURLAllowlist []string
	Dialer            *websocket.Dialer
	RawLogger         *rawlog.Logger
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
	if isRealtimeModel(config.Model) {
		config.SampleRate = realtimeSampleRate
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.Dialer == nil {
		config.Dialer = websocket.DefaultDialer
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
	if isRealtimeModel(p.config.Model) {
		return p.startRealtimeStream(ctx, request)
	}
	streamCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	s := &stream{
		ctx: streamCtx, cancel: cancel, config: p.config,
		request: request, chunks: make(chan tts.AudioChunk, 16), done: make(chan struct{}),
	}
	go s.run()
	return s, nil
}

func (p *Provider) startRealtimeStream(ctx context.Context, request tts.Request) (tts.Stream, error) {
	endpoint, err := realtimeEndpoint(p.config.BaseURL, p.config.Model)
	if err != nil {
		return nil, err
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, p.config.Timeout)
	conn, _, err := p.config.Dialer.DialContext(dialCtx, endpoint, http.Header{"Authorization": []string{"Bearer " + p.config.APIKey}})
	cancelDial()
	if err != nil {
		return nil, fmt.Errorf("connect Qwen TTS realtime: %w", err)
	}
	streamCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	s := &realtimeStream{ctx: streamCtx, cancel: cancel, conn: conn, config: p.config, request: request, rawLogger: p.config.RawLogger, chunks: make(chan tts.AudioChunk, 16), done: make(chan struct{})}
	if err := s.sendSession(); err != nil {
		cancel()
		_ = conn.Close()
		return nil, fmt.Errorf("configure Qwen TTS realtime session: %w", err)
	}
	go s.run()
	return s, nil
}

type realtimeStream struct {
	ctx        context.Context
	cancel     context.CancelFunc
	conn       *websocket.Conn
	config     Config
	request    tts.Request
	rawLogger  *rawlog.Logger
	chunks     chan tts.AudioChunk
	done       chan struct{}
	writeMu    sync.Mutex
	stateMu    sync.Mutex
	result     tts.Result
	err        error
	sequence   int64
	totalBytes int64
	eventSeq   uint64
}

func (s *realtimeStream) Chunks() <-chan tts.AudioChunk { return s.chunks }

func (s *realtimeStream) sendSession() error {
	voice := firstNonEmpty(s.request.VoiceID, s.config.Voice)
	for _, event := range []map[string]any{
		{"type": "session.update", "session": map[string]any{"voice": voice, "language_type": "Auto", "response_format": "pcm", "sample_rate": s.config.SampleRate, "mode": "server_commit"}},
		{"type": "input_text_buffer.append", "text": s.request.Text},
		{"type": "input_text_buffer.commit"},
	} {
		if err := s.write(event); err != nil {
			return err
		}
	}
	return nil
}

func (s *realtimeStream) write(value map[string]any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.eventSeq++
	value["event_id"] = fmt.Sprintf("event_%d", s.eventSeq)
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return err
	}
	_ = s.rawLogger.WriteJSON(s.request.SessionID, "tts", "request", rawEventType(data), data)
	return nil
}

func (s *realtimeStream) run() {
	defer func() {
		if s.ctx.Err() != nil && s.err == nil {
			s.setError(s.ctx.Err())
		}
		if s.err == nil {
			if s.sequence == 0 {
				s.setError(ErrNoAudio)
			} else {
				s.stateMu.Lock()
				s.result = tts.Result{Provider: s.config.Provider, Model: s.config.Model, AudioDuration: pcmDuration(s.totalBytes, s.config.SampleRate)}
				s.stateMu.Unlock()
			}
		}
		s.cancel()
		_ = s.conn.Close()
		close(s.chunks)
		close(s.done)
	}()
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			if s.ctx.Err() == nil {
				s.setError(fmt.Errorf("read Qwen TTS realtime event: %w", err))
			}
			return
		}
		_ = s.rawLogger.WriteJSON(s.request.SessionID, "tts", "response", rawEventType(data), data)
		var event realtimeEvent
		if err := json.Unmarshal(data, &event); err != nil {
			s.setError(fmt.Errorf("decode Qwen TTS realtime event: %w", err))
			return
		}
		switch event.Type {
		case "response.audio.delta":
			audio, err := base64.StdEncoding.DecodeString(event.Delta)
			if err != nil {
				s.setError(fmt.Errorf("decode Qwen TTS realtime audio: %w", err))
				return
			}
			if len(audio) == 0 {
				continue
			}
			s.sequence++
			s.totalBytes += int64(len(audio))
			select {
			case s.chunks <- tts.AudioChunk{SequenceNo: s.sequence, Encoding: "pcm_s16le", Data: audio}:
			case <-s.ctx.Done():
				return
			}
		case "error":
			s.setError(fmt.Errorf("Qwen TTS realtime failed: %s", firstNonEmpty(event.Error.Message, event.Message, event.Error.Code)))
			return
		case "response.done":
			if err := s.write(map[string]any{"type": "session.finish"}); err != nil {
				s.setError(fmt.Errorf("finish Qwen TTS realtime session: %w", err))
				return
			}
		case "session.finished":
			return
		}
	}
}

func (s *realtimeStream) Finish(ctx context.Context) (tts.Result, error) {
	select {
	case <-s.done:
		return s.finalResult()
	default:
	}
	select {
	case <-s.done:
		return s.finalResult()
	case <-ctx.Done():
		s.cancel()
		_ = s.conn.Close()
		return tts.Result{}, ctx.Err()
	}
}

func (s *realtimeStream) Close() error { s.cancel(); _ = s.conn.Close(); <-s.done; return nil }

func (s *realtimeStream) setError(err error) {
	s.stateMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.stateMu.Unlock()
}
func (s *realtimeStream) finalResult() (tts.Result, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.result, s.err
}

type realtimeEvent struct {
	Type    string `json:"type"`
	Delta   string `json:"delta"`
	Message string `json:"message"`
	Error   struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func rawEventType(data []byte) string {
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &event); err != nil || event.Type == "" {
		return "unknown"
	}
	return event.Type
}

type stream struct {
	ctx     context.Context
	cancel  context.CancelFunc
	config  Config
	request tts.Request
	chunks  chan tts.AudioChunk
	done    chan struct{}

	stateMu sync.Mutex
	result  tts.Result
	err     error
}

func (s *stream) Chunks() <-chan tts.AudioChunk { return s.chunks }

func (s *stream) Finish(ctx context.Context) (tts.Result, error) {
	if result, err, completed := s.completedResult(); completed {
		return result, err
	}
	select {
	case <-s.done:
		return s.finalResult()
	case <-ctx.Done():
		if result, err, completed := s.completedResult(); completed {
			return result, err
		}
		s.cancel()
		return tts.Result{}, ctx.Err()
	}
}

func (s *stream) completedResult() (tts.Result, error, bool) {
	select {
	case <-s.done:
		result, err := s.finalResult()
		return result, err, true
	default:
		return tts.Result{}, nil, false
	}
}

func (s *stream) finalResult() (tts.Result, error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.result, s.err
}

func (s *stream) Close() error {
	s.cancel()
	<-s.done
	return nil
}

func (s *stream) run() {
	defer func() {
		if err := s.ctx.Err(); err != nil {
			s.setError(err)
		}
		s.cancel()
		close(s.chunks)
		close(s.done)
	}()
	input := generationInput{
		Text:  s.request.Text,
		Voice: firstNonEmpty(s.request.VoiceID, s.config.Voice),
	}
	if isCosyVoiceModel(s.config.Model) {
		input.Instruction = languageInstruction(s.request.TargetLanguage)
	} else {
		input.LanguageType = languageType(s.request.TargetLanguage)
	}
	requestBody := generationRequest{Model: s.config.Model, Input: input}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		s.setError(fmt.Errorf("encode TTS request: %w", err))
		return
	}
	_ = s.config.RawLogger.WriteJSON(s.request.SessionID, "tts", "request", "generation", encoded)
	endpoint := ttsEndpoint(s.config.BaseURL, s.config.Model)
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
		_ = s.config.RawLogger.WriteJSON(s.request.SessionID, "tts", "response", "http.error", body)
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
		_ = s.config.RawLogger.WriteJSON(s.request.SessionID, "tts", "response", "sse.data", []byte(line))
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
		if len(data) == 0 {
			s.setError(ErrNoAudio)
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
	if sequence == 0 {
		s.setError(ErrNoAudio)
		return
	}
	s.stateMu.Lock()
	s.result = tts.Result{Provider: s.config.Provider, Model: s.config.Model, AudioDuration: pcmDuration(totalBytes, s.config.SampleRate)}
	s.stateMu.Unlock()
}

func (s *stream) downloadAudio(rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || !allowedAudioHost(u.Hostname(), s.config.AudioURLAllowlist) {
		return nil, fmt.Errorf("%w: %q", ErrAudioURLNotAllowed, rawURL)
	}
	client := *s.config.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Qwen TTS audio request: %w", err)
	}
	resp, err := client.Do(req)
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

func allowedAudioHost(host string, allowlist []string) bool {
	for _, allowed := range allowlist {
		if strings.EqualFold(strings.TrimSpace(allowed), host) {
			return true
		}
	}
	return false
}

func (s *stream) setError(err error) {
	s.stateMu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.stateMu.Unlock()
}

func ttsEndpoint(base, model string) string {
	base = strings.TrimRight(base, "/")
	if isCosyVoiceModel(model) {
		if strings.HasSuffix(base, "/services/audio/tts/SpeechSynthesizer") {
			return base
		}
		return base + "/services/audio/tts/SpeechSynthesizer"
	}
	if strings.HasSuffix(base, "/services/aigc/multimodal-generation/generation") {
		return base
	}
	return base + "/services/aigc/multimodal-generation/generation"
}

func isRealtimeModel(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), realtimeModel)
}

func realtimeEndpoint(raw, model string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss" && u.Scheme != "http" && u.Scheme != "https") {
		return "", ErrEndpointRequired
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	}
	path := strings.TrimSuffix(u.Path, "/")
	if !strings.HasSuffix(path, "/api-ws/v1/realtime") {
		for _, suffix := range []string{"/compatible-mode/v1", "/api/v1"} {
			if strings.HasSuffix(path, suffix) {
				path = strings.TrimSuffix(path, suffix)
				break
			}
		}
		path += "/api-ws/v1/realtime"
	}
	u.Path = path
	query := u.Query()
	query.Set("model", model)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func isCosyVoiceModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "cosyvoice-")
}

func languageInstruction(language string) string {
	labels := map[string]string{
		"zh": "中文", "en": "英语", "ja": "日语", "ko": "韩语",
		"fr": "法语", "de": "德语", "ru": "俄语", "pt": "葡萄牙语",
		"th": "泰语", "id": "印尼语", "vi": "越南语",
	}
	primary := strings.ToLower(language)
	if index := strings.IndexAny(primary, "-_"); index > 0 {
		primary = primary[:index]
	}
	if label := labels[primary]; label != "" {
		return "请用" + label + "自然地朗读。"
	}
	return "请自然地朗读。"
}

func languageType(language string) string {
	primary := strings.ToLower(strings.TrimSpace(language))
	if index := strings.IndexAny(primary, "-_"); index >= 0 {
		primary = primary[:index]
	}
	languages := map[string]string{
		"zh": "Chinese",
		"en": "English",
		"de": "German",
		"it": "Italian",
		"pt": "Portuguese",
		"es": "Spanish",
		"ja": "Japanese",
		"ko": "Korean",
		"fr": "French",
		"ru": "Russian",
	}
	if value := languages[primary]; value != "" {
		return value
	}
	// Qwen TTS rejects unknown BCP-47 tags. Auto lets the provider infer the
	// spoken language without leaking application locale syntax across the adapter.
	return "Auto"
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
	Instruction  string `json:"instruction,omitempty"`
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
