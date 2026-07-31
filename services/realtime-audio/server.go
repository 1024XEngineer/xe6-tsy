package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/localruntime"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/vad"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

const (
	defaultAddr           = ":8090"
	minTicketSecretBytes  = 32
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpWriteTimeout      = 45 * time.Second
	httpIdleTimeout       = 60 * time.Second
)

type processConfig struct {
	Addr          string
	TicketSecret  string
	SkipTTSTrack  bool
	DownlinkCodec string
}

func loadProcessConfig(getenv func(string) string) (processConfig, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	addr := strings.TrimSpace(getenv("REALTIME_ADDR"))
	if addr == "" {
		addr = defaultAddr
	}
	ticketSecret := strings.TrimSpace(getenv("REALTIME_TICKET_SECRET"))
	if len([]byte(ticketSecret)) < minTicketSecretBytes {
		return processConfig{}, fmt.Errorf("REALTIME_TICKET_SECRET must contain at least %d bytes", minTicketSecretBytes)
	}
	// Default skip TTS track for Chrome subtitle-only demos.
	// Set REALTIME_TTS_DOWNLINK=opus to attach a browser-playable Opus send track.
	downlink := strings.ToLower(strings.TrimSpace(getenv("REALTIME_TTS_DOWNLINK")))
	skipTTS := downlink != "opus"
	codec := ""
	if downlink == "opus" {
		codec = "opus"
	}
	return processConfig{
		Addr: addr, TicketSecret: ticketSecret, SkipTTSTrack: skipTTS, DownlinkCodec: codec,
	}, nil
}

func newControlPlaneHandler(ticketSecret string) (http.Handler, error) {
	cfg, err := loadProcessConfig(func(key string) string {
		switch key {
		case "REALTIME_TICKET_SECRET":
			return ticketSecret
		default:
			return os.Getenv(key)
		}
	})
	if err != nil {
		return nil, err
	}
	return newControlPlaneHandlerWithConfig(cfg)
}

func newControlPlaneHandlerWithConfig(cfg processConfig) (http.Handler, error) {
	now := func() time.Time { return time.Now().UTC() }

	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte(cfg.TicketSecret),
		TTL:    time.Minute,
		Now:    now,
	})
	if err != nil {
		return nil, fmt.Errorf("configure ticket codec: %w", err)
	}
	tickets, err := webrtc.NewHMACTicketValidator(codec)
	if err != nil {
		return nil, fmt.Errorf("configure ticket validator: %w", err)
	}

	factory, err := webrtc.NewPionTransportFactory(webrtc.PionTransportConfig{
		ICEServers: []webrtc.ICEServerConfig{{
			URLs: []string{"stun:stun.l.google.com:19302"},
		}},
		Media: webrtc.MediaConfig{
			SkipTTSTrack:  cfg.SkipTTSTrack,
			DownlinkCodec: cfg.DownlinkCodec,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("configure pion transport factory: %w", err)
	}
	connections := webrtc.NewMemoryConnectionManager(factory)

	signaling, err := webrtc.NewSignalingService(webrtc.Dependencies{
		Tickets:     tickets,
		Connections: connections,
		Now:         now,
	})
	if err != nil {
		return nil, fmt.Errorf("configure signaling: %w", err)
	}

	runtimeBridge := &localruntime.LifecycleRuntimeBridge{}
	languages := localruntime.StaticLanguageConfigReader{Source: "zh-CN", Target: "en-US", Now: now}
	finalTurns := localruntime.DataChannelFinalTurnSink{Media: connections}
	usage := &localruntime.MemoryUsageSink{}
	var audioSink pipeline.AudioChunkSink = localruntime.DiscardAudioSink{}
	if !cfg.SkipTTSTrack {
		audioSink = localruntime.PlaybackAudioSink{Media: connections}
	}

	manager, err := runtime.NewManagerFromEnvironment(mockOfflineProviders(), runtime.Dependencies{
		FrameSources: localruntime.WebRTCFrameSources{
			Media:          connections,
			SourceLanguage: "zh-CN",
		},
		NewSegmenter: func() (*vad.Segmenter, error) {
			return vad.NewSegmenter(localruntime.EnergySpeechClassifier{}, vad.Options{
				SilenceAfter: 400 * time.Millisecond,
				MaxDuration:  12 * time.Second,
			})
		},
		Languages:  languages,
		FinalTurns: finalTurns,
		Usage:      usage,
		Audio:      audioSink,
		Runtime:    runtimeBridge,
		VoiceID:    "Cherry",
		Now:        now,
	})
	if err != nil {
		return nil, fmt.Errorf("configure runtime manager: %w", err)
	}

	lifecycle, err := session.NewLifecycleService(session.Dependencies{
		Sessions:    localruntime.TrustSessionReader{},
		Runtimes:    session.NewMemoryRuntimeRepository(),
		Pipelines:   manager,
		Connections: connections,
		Now:         now,
	})
	if err != nil {
		return nil, fmt.Errorf("configure lifecycle: %w", err)
	}
	runtimeBridge.Set(lifecycle)

	handler, err := controlplane.New(controlplane.Dependencies{
		Lifecycle:   lifecycle,
		Signaling:   signaling,
		Connections: connections,
		Tickets:     tickets,
		Config: localruntime.StaticWebRTCConfig{
			ICEServers: []controlplane.ICEServer{{
				URLs: []string{"stun:stun.l.google.com:19302"},
			}},
			Now: now,
		},
		Now: now,
	})
	if err != nil {
		return nil, fmt.Errorf("configure control-plane: %w", err)
	}
	return handler, nil
}

func mockOfflineProviders() config.Providers {
	return config.Providers{
		ASR: asr.NewFakeProvider(asr.FakeProviderConfig{
			Final: asr.FinalResult{
				Text:           "你好",
				SourceLanguage: "zh-CN",
				Provider:       "mock-asr",
				Model:          "fake",
			},
		}),
		Translation: &translate.FakeProvider{
			Result: translate.Result{Text: "Hello", Provider: "mock-llm", Model: "fake"},
		},
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{
			Chunks: []tts.AudioChunk{{SequenceNo: 1, Data: []byte{0, 0, 0, 0}}},
			Result: tts.Result{Provider: "mock-tts", Model: "fake"},
		}),
	}
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}
