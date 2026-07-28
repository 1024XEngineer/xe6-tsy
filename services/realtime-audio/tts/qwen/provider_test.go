package qwen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

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
