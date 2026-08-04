package qwen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/gorilla/websocket"
)

func TestProviderMapsRealtimeEvents(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		seenEventIDs := make(map[string]struct{})
		if _, data, err := conn.ReadMessage(); err != nil {
			t.Errorf("read session update: %v", err)
			return
		} else {
			var event map[string]any
			if json.Unmarshal(data, &event) != nil || event["type"] != "session.update" {
				t.Errorf("session update = %s", data)
			}
			assertUniqueEventID(t, event, seenEventIDs)
		}
		_ = conn.WriteJSON(map[string]any{"type": "session.updated"})
		_ = conn.WriteJSON(map[string]any{"type": "input_audio_buffer.speech_started", "audio_start_ms": 100})
		if _, data, err := conn.ReadMessage(); err != nil {
			t.Errorf("read audio append: %v", err)
			return
		} else {
			var event map[string]any
			if json.Unmarshal(data, &event) != nil || event["type"] != "input_audio_buffer.append" {
				t.Errorf("audio append = %s", data)
			}
			assertUniqueEventID(t, event, seenEventIDs)
			encodedAudio, _ := event["audio"].(string)
			decoded, decodeErr := base64.StdEncoding.DecodeString(encodedAudio)
			if decodeErr != nil || string(decoded) != "pcm" {
				t.Errorf("audio payload = %q, err=%v", decoded, decodeErr)
			}
		}
		_ = conn.WriteJSON(map[string]any{"type": "conversation.item.input_audio_transcription.text", "language": "zh", "stash": "你"})
		if _, data, err := conn.ReadMessage(); err != nil {
			t.Errorf("read finish: %v", err)
			return
		} else {
			var event map[string]any
			if json.Unmarshal(data, &event) != nil || event["type"] != "session.finish" {
				t.Errorf("finish event = %s", data)
			}
			assertUniqueEventID(t, event, seenEventIDs)
		}
		_ = conn.WriteJSON(map[string]any{"type": "input_audio_buffer.speech_stopped", "audio_end_ms": 1100})
		_ = conn.WriteJSON(map[string]any{"type": "conversation.item.input_audio_transcription.completed", "language": "zh", "transcript": "你好"})
		_ = conn.WriteJSON(map[string]any{"type": "session.finished"})
	}))
	defer server.Close()

	provider, err := NewProvider(Config{APIKey: "test-key", WebSocketURL: "ws" + strings.TrimPrefix(server.URL, "http"), Model: "qwen3-asr-flash-realtime", SilenceDuration: 400 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	stream, err := provider.StartStream(context.Background(), asr.StreamRequest{SourceLanguage: "zh-CN"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	if err := stream.PushAudio(context.Background(), []byte("pcm")); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	result, err := stream.Finish(context.Background())
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if result.Text != "你好" || result.SourceLanguage != "zh-CN" || result.AudioDuration != time.Second {
		t.Fatalf("result = %#v", result)
	}
	var events []asr.Event
	for event := range stream.Events() {
		events = append(events, event)
	}
	if len(events) != 2 || events[0].Type != asr.EventPartial || events[1].Type != asr.EventFinal {
		t.Fatalf("events = %#v", events)
	}
}

func TestFinishPrefersCompletedResultOverCanceledContext(t *testing.T) {
	done := make(chan struct{})
	close(done)
	want := asr.FinalResult{Text: "final", SourceLanguage: "zh-CN"}
	stream := &stream{done: done, result: want}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := stream.Finish(ctx)
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if got != want {
		t.Fatalf("Finish() result = %#v, want %#v", got, want)
	}
}

func assertUniqueEventID(t *testing.T, event map[string]any, seen map[string]struct{}) {
	t.Helper()
	eventID, ok := event["event_id"].(string)
	if !ok || eventID == "" {
		t.Errorf("event_id = %#v", event["event_id"])
		return
	}
	if _, exists := seen[eventID]; exists {
		t.Errorf("duplicate event_id = %q", eventID)
		return
	}
	seen[eventID] = struct{}{}
}

func TestDeriveWebSocketURL(t *testing.T) {
	got := deriveWebSocketURL("https://workspace.cn-beijing.maas.aliyuncs.com/compatible-mode/v1")
	want := "wss://workspace.cn-beijing.maas.aliyuncs.com/api-ws/v1/realtime"
	if got != want {
		t.Fatalf("deriveWebSocketURL() = %q, want %q", got, want)
	}
}

func TestWriteClosesConnectionWhenContextIsCanceled(t *testing.T) {
	conn := &blockingWriteConn{closed: make(chan struct{})}
	stream := &stream{conn: conn}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- stream.write(ctx, map[string]any{"type": "session.finish"}) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("write() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("write() did not stop after context cancellation")
	}
	select {
	case <-conn.closed:
	default:
		t.Fatal("write() did not close the WebSocket")
	}
}

func TestWriteDoesNotCloseConnectionAfterSuccessfulWrite(t *testing.T) {
	conn := &trackingWriteConn{closed: make(chan struct{})}
	stream := &stream{conn: conn}
	ctx, cancel := context.WithCancel(context.Background())
	if err := stream.write(ctx, map[string]any{"type": "session.finish"}); err != nil {
		t.Fatalf("write() error = %v", err)
	}
	cancel()
	select {
	case <-conn.closed:
		t.Fatal("write() cancellation closed the connection after returning")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestWriteReturnsCancellationAfterWriteCompletes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	conn := &cancelDuringWriteConn{cancel: cancel}
	stream := &stream{conn: conn}

	if err := stream.write(ctx, map[string]any{"type": "session.finish"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("write() error = %v, want context.Canceled", err)
	}
}

func TestFinishDoesNotBlockOnUnconsumedPartialEvents(t *testing.T) {
	const partialCount = eventBufferSize * 2
	messages := make([][]byte, 0, partialCount+2)
	for i := 0; i < partialCount; i++ {
		messages = append(messages, mustJSON(t, map[string]any{
			"type": "conversation.item.input_audio_transcription.text",
			"text": fmt.Sprintf("partial-%d", i),
		}))
	}
	messages = append(messages,
		mustJSON(t, map[string]any{
			"type":       "conversation.item.input_audio_transcription.completed",
			"transcript": "final",
		}),
		mustJSON(t, map[string]any{"type": "session.finished"}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &stream{
		conn:     &scriptedReadConn{messages: messages},
		cancel:   cancel,
		events:   make(chan asr.Event, eventBufferSize+1),
		done:     make(chan struct{}),
		readDone: make(chan struct{}),
	}
	go stream.readLoop(ctx)

	result, err := stream.Finish(context.Background())
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if result.Text != "final" {
		t.Fatalf("result.Text = %q, want final", result.Text)
	}

	var finalCount, partials int
	for event := range stream.Events() {
		switch event.Type {
		case asr.EventPartial:
			partials++
		case asr.EventFinal:
			finalCount++
		}
	}
	if finalCount != 1 {
		t.Fatalf("final event count = %d, want 1", finalCount)
	}
	if partials > eventBufferSize {
		t.Fatalf("partial event count = %d, want at most %d", partials, eventBufferSize)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

type trackingWriteConn struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func (*trackingWriteConn) WriteMessage(int, []byte) error { return nil }
func (*trackingWriteConn) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("not implemented")
}
func (c *trackingWriteConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}
func (*trackingWriteConn) SetWriteDeadline(time.Time) error { return nil }

type cancelDuringWriteConn struct {
	cancel context.CancelFunc
}

func (c *cancelDuringWriteConn) WriteMessage(int, []byte) error {
	c.cancel()
	return nil
}
func (*cancelDuringWriteConn) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("not implemented")
}
func (*cancelDuringWriteConn) Close() error                     { return nil }
func (*cancelDuringWriteConn) SetWriteDeadline(time.Time) error { return nil }

type scriptedReadConn struct {
	mu       sync.Mutex
	messages [][]byte
}

func (*scriptedReadConn) WriteMessage(int, []byte) error { return nil }
func (c *scriptedReadConn) ReadMessage() (int, []byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.messages) == 0 {
		return 0, nil, errors.New("no more scripted messages")
	}
	message := c.messages[0]
	c.messages = c.messages[1:]
	return websocket.TextMessage, message, nil
}
func (*scriptedReadConn) Close() error                     { return nil }
func (*scriptedReadConn) SetWriteDeadline(time.Time) error { return nil }

type blockingWriteConn struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func (c *blockingWriteConn) WriteMessage(int, []byte) error {
	<-c.closed
	return errors.New("connection closed")
}

func (*blockingWriteConn) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("not implemented")
}

func (c *blockingWriteConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (*blockingWriteConn) SetWriteDeadline(time.Time) error { return nil }

func TestSessionUpdateEventOmitsLanguageWhenEmpty(t *testing.T) {
	event := sessionUpdateEvent("", Config{SampleRate: 16000, DisableServerVAD: true})
	session, ok := event["session"].(map[string]any)
	if !ok {
		t.Fatalf("session missing: %#v", event)
	}
	transcription, ok := session["input_audio_transcription"].(map[string]any)
	if !ok {
		t.Fatalf("transcription missing: %#v", session)
	}
	if _, present := transcription["language"]; present {
		t.Fatalf("language should be omitted for auto-detect, got %#v", transcription)
	}
}

func TestSessionUpdateEventSetsNullTurnDetectionWhenDisabled(t *testing.T) {
	event := sessionUpdateEvent("zh-CN", Config{SampleRate: 16000, DisableServerVAD: true})
	session, ok := event["session"].(map[string]any)
	if !ok {
		t.Fatalf("session missing: %#v", event)
	}
	if session["turn_detection"] != nil {
		t.Fatalf("turn_detection = %#v, want JSON null", session["turn_detection"])
	}

	withVAD := sessionUpdateEvent("zh-CN", Config{
		SampleRate: 16000, VADThreshold: 0.2, SilenceDuration: 400 * time.Millisecond,
	})
	session, ok = withVAD["session"].(map[string]any)
	if !ok {
		t.Fatalf("session missing: %#v", withVAD)
	}
	detection, ok := session["turn_detection"].(map[string]any)
	if !ok || detection["type"] != "server_vad" {
		t.Fatalf("turn_detection = %#v", session["turn_detection"])
	}
}

func TestStreamCloseShutsDownOnce(t *testing.T) {
	conn := &countingCloseConn{closed: make(chan struct{})}
	events := make(chan asr.Event, eventBufferSize+1)
	done := make(chan struct{})
	readDone := make(chan struct{})
	close(readDone)
	canceled := make(chan struct{})
	var cancelOnce sync.Once
	stream := &stream{
		conn:     conn,
		cancel:   func() { cancelOnce.Do(func() { close(canceled) }) },
		events:   events,
		done:     done,
		readDone: readDone,
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("Close() did not cancel the stream context")
	}
	select {
	case <-done:
	default:
		t.Fatal("Close() did not close the done channel")
	}
	if _, open := <-events; open {
		t.Fatal("Close() left the events channel open")
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if conn.closeCount != 1 {
		t.Fatalf("connection close count = %d, want 1", conn.closeCount)
	}
}

func TestStreamSetErrorKeepsFirstError(t *testing.T) {
	first := errors.New("first error")
	second := errors.New("second error")
	stream := &stream{}

	stream.setError(first)
	stream.setError(second)

	if _, got := stream.finalResult(); !errors.Is(got, first) {
		t.Fatalf("finalResult() error = %v, want first error", got)
	}
}

type countingCloseConn struct {
	closed     chan struct{}
	closeCount int
}

func (*countingCloseConn) WriteMessage(int, []byte) error { return nil }
func (*countingCloseConn) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("not implemented")
}
func (c *countingCloseConn) Close() error {
	c.closeCount++
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}
func (*countingCloseConn) SetWriteDeadline(time.Time) error { return nil }

func TestManualModeFinishSendsCommitBeforeSessionFinish(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	writes := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var event map[string]any
			if json.Unmarshal(data, &event) != nil {
				continue
			}
			typ, _ := event["type"].(string)
			writes <- typ
			switch typ {
			case "session.update":
				_ = conn.WriteJSON(map[string]any{"type": "session.updated"})
			case "input_audio_buffer.commit":
				_ = conn.WriteJSON(map[string]any{"type": "input_audio_buffer.committed"})
			case "session.finish":
				_ = conn.WriteJSON(map[string]any{
					"type":     "conversation.item.input_audio_transcription.completed",
					"language": "zh", "transcript": "你好",
				})
				_ = conn.WriteJSON(map[string]any{"type": "session.finished"})
				return
			}
		}
	}))
	defer server.Close()

	provider, err := NewProvider(Config{
		APIKey:           "test-key",
		WebSocketURL:     "ws" + strings.TrimPrefix(server.URL, "http"),
		Model:            "qwen3-asr-flash-realtime",
		DisableServerVAD: true,
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	stream, err := provider.StartStream(context.Background(), asr.StreamRequest{SourceLanguage: "zh-CN"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	if err := stream.PushAudio(context.Background(), []byte("pcm")); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
	result, err := stream.Finish(context.Background())
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if result.Text != "你好" {
		t.Fatalf("result = %#v", result)
	}

	var seen []string
	timeout := time.After(2 * time.Second)
	for len(seen) < 4 {
		select {
		case typ := <-writes:
			seen = append(seen, typ)
		case <-timeout:
			t.Fatalf("writes = %v, want update/append/commit/finish", seen)
		}
	}
	if seen[0] != "session.update" || seen[1] != "input_audio_buffer.append" ||
		seen[2] != "input_audio_buffer.commit" || seen[3] != "session.finish" {
		t.Fatalf("write order = %v", seen)
	}
}

func TestNewProviderValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr error
	}{
		{name: "missing api key", config: Config{WebSocketURL: "wss://example.com"}, wantErr: ErrAPIKeyRequired},
		{name: "missing endpoint", config: Config{APIKey: "test-key"}, wantErr: ErrEndpointRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewProvider(test.config); !errors.Is(err, test.wantErr) {
				t.Fatalf("NewProvider() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewProviderAppliesDefaultsAndDerivesEndpoint(t *testing.T) {
	provider, err := NewProvider(Config{APIKey: "test-key", BaseURL: "https://example.com"})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	if provider.config.WebSocketURL != "wss://example.com/api-ws/v1/realtime" {
		t.Fatalf("WebSocketURL = %q, want derived realtime endpoint", provider.config.WebSocketURL)
	}
	if provider.config.Model != defaultModel {
		t.Fatalf("Model = %q, want %q", provider.config.Model, defaultModel)
	}
	if provider.config.SampleRate != 16000 {
		t.Fatalf("SampleRate = %d, want 16000", provider.config.SampleRate)
	}
	if provider.config.SilenceDuration != 500*time.Millisecond {
		t.Fatalf("SilenceDuration = %v, want 500ms", provider.config.SilenceDuration)
	}
	if provider.config.Provider != "aliyun" {
		t.Fatalf("Provider = %q, want aliyun", provider.config.Provider)
	}
	if provider.config.Dialer == nil {
		t.Fatal("Dialer = nil, want default dialer")
	}
}

func TestDeriveWebSocketURLVariants(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "empty", base: "", want: ""},
		{name: "invalid host", base: "not a url", want: ""},
		{name: "compatible mode", base: "https://example.com/compatible-mode/v1", want: "wss://example.com/api-ws/v1/realtime"},
		{name: "api v1", base: "https://example.com/api/v1", want: "wss://example.com/api-ws/v1/realtime"},
		{name: "custom path", base: "https://example.com/custom", want: "wss://example.com/custom/api-ws/v1/realtime"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := deriveWebSocketURL(test.base); got != test.want {
				t.Fatalf("deriveWebSocketURL(%q) = %q, want %q", test.base, got, test.want)
			}
		})
	}
}

func TestRealtimeEndpoint(t *testing.T) {
	if _, err := realtimeEndpoint("not a url", "model_01"); !errors.Is(err, ErrEndpointRequired) {
		t.Fatalf("realtimeEndpoint() error = %v, want endpoint required", err)
	}
	endpoint, err := realtimeEndpoint("wss://example.com/api-ws/v1/realtime?key=1", "model_01")
	if err != nil {
		t.Fatalf("realtimeEndpoint() error = %v", err)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	if parsed.Query().Get("model") != "model_01" || parsed.Query().Get("key") != "1" {
		t.Fatalf("endpoint query = %v, want model and key preserved", parsed.Query())
	}
}

func TestLanguageCodeNormalization(t *testing.T) {
	tests := []struct {
		language string
		want     string
	}{
		{language: "zh-CN", want: "zh"},
		{language: "zh_CN", want: "zh"},
		{language: "en-US", want: "en"},
		{language: "ja", want: "ja"},
	}
	for _, test := range tests {
		if got := languageCode(test.language); got != test.want {
			t.Fatalf("languageCode(%q) = %q, want %q", test.language, got, test.want)
		}
	}
}

func TestHandleEventRejectsInvalidJSON(t *testing.T) {
	stream := &stream{}
	if err := stream.handleEvent([]byte("{invalid")); err == nil {
		t.Fatal("handleEvent() invalid JSON succeeded")
	}
}

func TestHandleEventReturnsProviderFailure(t *testing.T) {
	stream := &stream{}
	data := mustJSON(t, map[string]any{
		"type":  "conversation.item.input_audio_transcription.failed",
		"error": map[string]any{"message": "provider boom"},
	})
	if err := stream.handleEvent(data); err == nil {
		t.Fatal("handleEvent() failed event succeeded")
	}
}

func TestHandleEventSetsFinalResult(t *testing.T) {
	stream := &stream{
		sourceLanguage: "zh-CN", provider: "aliyun", model: "model_01",
		events: make(chan asr.Event, 1),
	}
	data := mustJSON(t, map[string]any{
		"type": "conversation.item.input_audio_transcription.completed", "transcript": "你好",
	})
	if err := stream.handleEvent(data); err != nil {
		t.Fatalf("handleEvent() error = %v", err)
	}
	result, err := stream.finalResult()
	if err != nil {
		t.Fatalf("finalResult() error = %v", err)
	}
	if result.Text != "你好" || result.SourceLanguage != "zh-CN" || result.AudioDuration != 0 {
		t.Fatalf("final result = %#v", result)
	}
	select {
	case event := <-stream.events:
		if event.Type != asr.EventFinal {
			t.Fatalf("emitted event = %#v, want final", event)
		}
	default:
		t.Fatal("no final event emitted")
	}
}

func TestHandleEventFallsBackToEventLanguage(t *testing.T) {
	stream := &stream{
		provider: "aliyun", model: "model_01",
		events: make(chan asr.Event, 1),
	}
	data := mustJSON(t, map[string]any{
		"type": "conversation.item.input_audio_transcription.completed", "language": "zh", "transcript": "你好",
	})
	if err := stream.handleEvent(data); err != nil {
		t.Fatalf("handleEvent() error = %v", err)
	}
	result, err := stream.finalResult()
	if err != nil {
		t.Fatalf("finalResult() error = %v", err)
	}
	if result.SourceLanguage != "zh-CN" {
		t.Fatalf("SourceLanguage = %q, want zh-CN from event", result.SourceLanguage)
	}
}

func TestHandleEventPartialUsesStashFallback(t *testing.T) {
	stream := &stream{events: make(chan asr.Event, 2)}
	data := mustJSON(t, map[string]any{
		"type": "conversation.item.input_audio_transcription.text", "stash": "你",
	})
	if err := stream.handleEvent(data); err != nil {
		t.Fatalf("handleEvent() error = %v", err)
	}
	select {
	case event := <-stream.events:
		if event.Type != asr.EventPartial || event.Text != "你" {
			t.Fatalf("emitted partial = %#v", event)
		}
	default:
		t.Fatal("no partial event emitted")
	}
}

func TestEmitEventBackpressure(t *testing.T) {
	stream := &stream{events: make(chan asr.Event, 1)}
	stream.events <- asr.Event{Type: asr.EventPartial, Text: "fill"}
	if err := stream.emitEvent(asr.Event{Type: asr.EventPartial, Text: "drop"}); err != nil {
		t.Fatalf("emitEvent() partial on full buffer error = %v, want nil drop", err)
	}
	final := asr.Event{Type: asr.EventFinal, Final: &asr.FinalResult{}}
	if err := stream.emitEvent(final); err == nil {
		t.Fatal("emitEvent() final on full buffer succeeded")
	}
}

func TestPushAudioIgnoresEmptyChunk(t *testing.T) {
	stream := &stream{}
	if err := stream.PushAudio(context.Background(), nil); err != nil {
		t.Fatalf("PushAudio() error = %v", err)
	}
}

func TestWriteReturnsConnectionError(t *testing.T) {
	stream := &stream{conn: &failWriteConn{}}
	err := stream.write(context.Background(), map[string]any{"type": "session.finish"})
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("write() error = %v, want connection error", err)
	}
}

func TestFinishReturnsErrorWhenSessionFinishWriteFails(t *testing.T) {
	readDone := make(chan struct{})
	close(readDone)
	stream := &stream{
		conn:     &failWriteConn{},
		cancel:   func() {},
		events:   make(chan asr.Event, eventBufferSize+1),
		done:     make(chan struct{}),
		readDone: readDone,
	}
	if _, err := stream.Finish(context.Background()); err == nil {
		t.Fatal("Finish() error = nil, want write error")
	}
}

type failWriteConn struct{}

func (*failWriteConn) WriteMessage(int, []byte) error { return errors.New("write failed") }
func (*failWriteConn) ReadMessage() (int, []byte, error) {
	return 0, nil, errors.New("read failed")
}
func (*failWriteConn) Close() error                     { return nil }
func (*failWriteConn) SetWriteDeadline(time.Time) error { return nil }
