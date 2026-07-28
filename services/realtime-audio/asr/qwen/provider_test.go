package qwen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		if _, data, err := conn.ReadMessage(); err != nil {
			t.Errorf("read session update: %v", err)
			return
		} else {
			var event map[string]any
			if json.Unmarshal(data, &event) != nil || event["type"] != "session.update" {
				t.Errorf("session update = %s", data)
			}
		}
		_ = conn.WriteJSON(map[string]any{"type": "session.updated"})
		_ = conn.WriteJSON(map[string]any{"type": "input_audio_buffer.speech_started", "audio_start_ms": 100})
		if _, data, err := conn.ReadMessage(); err != nil {
			t.Errorf("read audio append: %v", err)
			return
		} else {
			var event struct {
				Type  string `json:"type"`
				Audio string `json:"audio"`
			}
			if json.Unmarshal(data, &event) != nil || event.Type != "input_audio_buffer.append" {
				t.Errorf("audio append = %s", data)
			}
			decoded, decodeErr := base64.StdEncoding.DecodeString(event.Audio)
			if decodeErr != nil || string(decoded) != "pcm" {
				t.Errorf("audio payload = %q, err=%v", decoded, decodeErr)
			}
		}
		_ = conn.WriteJSON(map[string]any{"type": "conversation.item.input_audio_transcription.text", "language": "zh", "stash": "你"})
		if _, data, err := conn.ReadMessage(); err != nil {
			t.Errorf("read finish: %v", err)
			return
		} else if !strings.Contains(string(data), `"session.finish"`) {
			t.Errorf("finish event = %s", data)
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
	if result.Text != "你好" || result.SourceLanguage != "zh" || result.AudioDuration != time.Second {
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

func TestDeriveWebSocketURL(t *testing.T) {
	got := deriveWebSocketURL("https://workspace.cn-beijing.maas.aliyuncs.com/compatible-mode/v1")
	want := "wss://workspace.cn-beijing.maas.aliyuncs.com/api-ws/v1/realtime"
	if got != want {
		t.Fatalf("deriveWebSocketURL() = %q, want %q", got, want)
	}
}
