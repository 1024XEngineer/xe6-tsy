package qwen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
	"github.com/gorilla/websocket"
)

func TestProviderStreamsQwenRealtimeAudio(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		if r.URL.Query().Get("model") != "qwen3-tts-flash-realtime" {
			t.Errorf("model = %q", r.URL.Query().Get("model"))
		}
		eventIDs := make(map[string]struct{})
		readEvent := func(label string) map[string]any {
			_, data, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read %s: %v", label, err)
				return nil
			}
			var event map[string]any
			if err := json.Unmarshal(data, &event); err != nil {
				t.Errorf("decode %s: %v", label, err)
				return nil
			}
			id, ok := event["event_id"].(string)
			if !ok || id == "" {
				t.Errorf("%s event_id = %#v", label, event["event_id"])
			} else if _, exists := eventIDs[id]; exists {
				t.Errorf("duplicate event_id %q", id)
			} else {
				eventIDs[id] = struct{}{}
			}
			return event
		}
		update := readEvent("session update")
		session, _ := update["session"].(map[string]any)
		if update["type"] != "session.update" || session["voice"] != "Cherry" || session["language_type"] != "Auto" || session["sample_rate"] != float64(realtimeSampleRate) {
			t.Errorf("update = %#v", update)
		}
		for i := 0; i < 2; i++ {
			readEvent("text event")
		}
		for _, audio := range [][]byte{{1, 2}, {3, 4}} {
			payload := map[string]any{"type": "response.audio.delta", "delta": base64.StdEncoding.EncodeToString(audio)}
			encoded, _ := json.Marshal(payload)
			_ = conn.WriteMessage(websocket.TextMessage, encoded)
		}
		done, _ := json.Marshal(map[string]any{"type": "response.done"})
		_ = conn.WriteMessage(websocket.TextMessage, done)
		readEvent("session finish")
		if len(eventIDs) != 4 {
			t.Errorf("event IDs = %d, want 4", len(eventIDs))
		}
		finished, _ := json.Marshal(map[string]any{"type": "session.finished"})
		_ = conn.WriteMessage(websocket.TextMessage, finished)
	}))
	defer server.Close()

	provider, err := NewProvider(Config{APIKey: "test-key", BaseURL: "ws" + strings.TrimPrefix(server.URL, "http"), Model: "qwen3-tts-flash-realtime", SampleRate: 16000})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	stream, err := provider.StartStream(context.Background(), tts.Request{Text: "hello", TargetLanguage: "fr-FR"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	var chunks []tts.AudioChunk
	for chunk := range stream.Chunks() {
		chunks = append(chunks, chunk)
	}
	result, err := stream.Finish(context.Background())
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if len(chunks) != 2 || string(chunks[1].Data) != string([]byte{3, 4}) {
		t.Fatalf("chunks = %#v", chunks)
	}
	if result.Model != "qwen3-tts-flash-realtime" || result.AudioDuration <= 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestProviderRealtimeDialUsesProviderTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()

	provider, err := NewProvider(Config{
		APIKey:     "test-key",
		BaseURL:    "ws" + strings.TrimPrefix(server.URL, "http"),
		Model:      realtimeModel,
		Timeout:    50 * time.Millisecond,
		Dialer:     &websocket.Dialer{},
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, startErr := provider.StartStream(context.Background(), tts.Request{Text: "hello"})
		result <- startErr
	}()
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("StartStream() unexpectedly connected")
		}
	case <-time.After(time.Second):
		t.Fatal("StartStream() exceeded the test deadline")
	}
}

func TestProviderStreamsQwenTTSAudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/services/aigc/multimodal-generation/generation" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-DashScope-SSE") != "enable" {
			t.Errorf("headers = %#v", r.Header)
		}
		var request struct {
			Model string `json:"model"`
			Input struct {
				Text         string `json:"text"`
				Voice        string `json:"voice"`
				LanguageType string `json:"language_type"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != "qwen3-tts-flash" || request.Input.Text != "hello" || request.Input.Voice != "Cherry" || request.Input.LanguageType != "English" {
			t.Errorf("request = %#v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + ttsEvent([]byte{1, 2}) + "\n\n"))
		_, _ = w.Write([]byte("data: " + ttsEvent([]byte{3, 4}) + "\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	provider, err := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL + "/api/v1"})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	stream, err := provider.StartStream(context.Background(), tts.Request{Text: "hello", TargetLanguage: "en-US"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	var chunks []tts.AudioChunk
	for chunk := range stream.Chunks() {
		chunks = append(chunks, chunk)
	}
	result, err := stream.Finish(context.Background())
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	if len(chunks) != 2 || chunks[0].SequenceNo != 1 || string(chunks[1].Data) != string([]byte{3, 4}) {
		t.Fatalf("chunks = %#v", chunks)
	}
	if result.Provider != "aliyun" || result.Model != "qwen3-tts-flash" {
		t.Fatalf("result = %#v", result)
	}
}

func TestCosyVoiceRequestUsesMultilingualInstruction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/services/audio/tts/SpeechSynthesizer" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var request struct {
			Model string `json:"model"`
			Input struct {
				Voice       string `json:"voice"`
				Instruction string `json:"instruction"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Model != "cosyvoice-v3.5-flash" || request.Input.Voice != "longanhuan_v3" || request.Input.Instruction != "请用日语自然地朗读。" {
			t.Errorf("request = %#v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + ttsEvent([]byte{1, 2}) + "\n\n"))
	}))
	defer server.Close()

	provider, err := NewProvider(Config{
		APIKey: "test-key", BaseURL: server.URL + "/api/v1",
		Model: "cosyvoice-v3.5-flash", Voice: "longanhuan_v3",
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	stream, err := provider.StartStream(context.Background(), tts.Request{Text: "hello", TargetLanguage: "ja-JP"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	for range stream.Chunks() {
	}
	if _, err := stream.Finish(context.Background()); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
}

func TestFinishPrefersCompletedResultOverCanceledContext(t *testing.T) {
	done := make(chan struct{})
	close(done)
	want := tts.Result{Provider: "aliyun", Model: "qwen3-tts-flash"}
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

func TestFinishCancellationDoesNotCloseChunksWhileWorkerSends(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for index := 0; index < 32; index++ {
			_, _ = w.Write([]byte("data: " + ttsEvent([]byte{byte(index)}) + "\n\n"))
		}
	}))
	defer server.Close()

	provider, err := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	providerStream, err := provider.StartStream(context.Background(), tts.Request{Text: "hello", TargetLanguage: "en-US"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	qwenStream := providerStream.(*stream)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for len(qwenStream.chunks) < cap(qwenStream.chunks) {
		select {
		case <-deadline.C:
			t.Fatal("TTS worker did not fill the chunk buffer")
		default:
			runtime.Gosched()
		}
	}

	finishCtx, cancelFinish := context.WithCancel(context.Background())
	cancelFinish()
	if _, err := providerStream.Finish(finishCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Finish() error = %v, want context.Canceled", err)
	}
	if err := providerStream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for range providerStream.Chunks() {
	}
}

func TestProviderRejectsEmptyAudioResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()

	provider, err := NewProvider(Config{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	stream, err := provider.StartStream(context.Background(), tts.Request{Text: "hello", TargetLanguage: "en-US"})
	if err != nil {
		t.Fatalf("StartStream() error = %v", err)
	}
	if _, err := stream.Finish(context.Background()); !errors.Is(err, ErrNoAudio) {
		t.Fatalf("Finish() error = %v, want ErrNoAudio", err)
	}
}

func TestDownloadAudioRequiresAllowlistedHost(t *testing.T) {
	stream := &stream{ctx: context.Background(), config: Config{HTTPClient: http.DefaultClient}}
	if _, err := stream.downloadAudio("http://127.0.0.1:12345/audio"); !errors.Is(err, ErrAudioURLNotAllowed) {
		t.Fatalf("downloadAudio() error = %v, want ErrAudioURLNotAllowed", err)
	}
}

func ttsEvent(audio []byte) string {
	data, _ := json.Marshal(generationResponse{Output: struct {
		Audio struct {
			Data string `json:"data"`
			URL  string `json:"url"`
		} `json:"audio"`
	}{}})
	var event map[string]any
	_ = json.Unmarshal(data, &event)
	event["output"] = map[string]any{"audio": map[string]any{"data": base64.StdEncoding.EncodeToString(audio)}}
	data, _ = json.Marshal(event)
	return string(data)
}
