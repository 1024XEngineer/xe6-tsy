package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/config"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
)

func secretEnv(secret string) func(string) string {
	return func(key string) string {
		switch key {
		case "REALTIME_TICKET_SECRET":
			return secret
		case "REALTIME_ADDR":
			return ":18090"
		default:
			return ""
		}
	}
}

func TestLoadProcessConfigDefaultsAndValidatesSecret(t *testing.T) {
	cfg, err := loadProcessConfig(func(key string) string {
		if key == "REALTIME_TICKET_SECRET" {
			return strings.Repeat("s", 32)
		}
		return ""
	})
	if err != nil {
		t.Fatalf("loadProcessConfig() error = %v", err)
	}
	if cfg.Addr != defaultAddr {
		t.Fatalf("Addr = %q, want %q", cfg.Addr, defaultAddr)
	}
	if !cfg.SkipTTSTrack {
		t.Fatal("SkipTTSTrack = false, want true by default")
	}
	if cfg.DownlinkMode != "none" || cfg.DownlinkCodec != "none" {
		t.Fatalf("default downlink = mode=%q codec=%q, want none/none", cfg.DownlinkMode, cfg.DownlinkCodec)
	}
	if cfg.SourceLanguage != "zh-CN" || cfg.TargetLanguage != "en-US" {
		t.Fatalf("languages = %s→%s", cfg.SourceLanguage, cfg.TargetLanguage)
	}
	if cfg.CommandConfigTimeout != defaultCommandConfigTimeout {
		t.Fatalf("command config timeout = %s, want %s", cfg.CommandConfigTimeout, defaultCommandConfigTimeout)
	}

	if _, err := loadProcessConfig(func(string) string { return "" }); err == nil {
		t.Fatal("loadProcessConfig() error = nil, want secret validation error")
	}
	t.Setenv("REALTIME_TICKET_SECRET", "")
	if _, err := loadProcessConfig(nil); err == nil {
		t.Fatal("loadProcessConfig(nil) error = nil, want secret validation error")
	}
}

func TestLoadProcessConfigReadsCommandAPISettings(t *testing.T) {
	token := strings.Repeat("c", minCommandTokenBytes)
	cfg, err := loadProcessConfig(func(key string) string {
		switch key {
		case "REALTIME_TICKET_SECRET":
			return strings.Repeat("s", 32)
		case "LINGOW_API_BASE_URL":
			return "http://api:8080"
		case "LINGOW_COMMAND_SYSTEM_TOKEN":
			return token
		case "COMMAND_CONFIG_TIMEOUT_MS":
			return "1750"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("loadProcessConfig() error = %v", err)
	}
	if cfg.APIBaseURL != "http://api:8080" || cfg.CommandToken != token || cfg.CommandConfigTimeout != 1750*time.Millisecond {
		t.Fatalf("command API config = %#v", cfg)
	}
}

func TestLoadProcessConfigRejectsIncompleteCommandAPISettings(t *testing.T) {
	tests := []map[string]string{
		{"LINGOW_API_BASE_URL": "http://api:8080"},
		{"LINGOW_COMMAND_SYSTEM_TOKEN": strings.Repeat("c", minCommandTokenBytes)},
		{"LINGOW_API_BASE_URL": "http://api:8080", "LINGOW_COMMAND_SYSTEM_TOKEN": "short"},
		{"COMMAND_CONFIG_TIMEOUT_MS": "none"},
		{"COMMAND_CONFIG_TIMEOUT_MS": "0"},
	}
	for _, values := range tests {
		_, err := loadProcessConfig(func(key string) string {
			if key == "REALTIME_TICKET_SECRET" {
				return strings.Repeat("s", 32)
			}
			return values[key]
		})
		if err == nil {
			t.Fatalf("loadProcessConfig(%#v) error = nil", values)
		}
	}
}

func TestApplySubtitleOnlyOverridesForcesMockTTS(t *testing.T) {
	cfg := processConfig{ForceMockTTS: true}
	providers := config.ProviderConfig{
		ASR:         config.ASRConfig{Provider: config.ProviderAliyun},
		Translation: config.TranslationConfig{Provider: config.ProviderAliyun},
		TTS:         config.TTSConfig{Provider: config.ProviderAliyun},
	}
	got := applySubtitleOnlyOverrides(cfg, providers)
	if got.TTS.Provider != config.ProviderMock {
		t.Fatalf("TTS provider = %q, want mock", got.TTS.Provider)
	}
	if got.ASR.Provider != config.ProviderAliyun || got.Translation.Provider != config.ProviderAliyun {
		t.Fatalf("ASR/LLM should stay aliyun: %#v", got)
	}

	cfg.ForceMockTTS = false
	got = applySubtitleOnlyOverrides(cfg, providers)
	if got.TTS.Provider != config.ProviderAliyun {
		t.Fatalf("TTS provider = %q, want aliyun when audio downlink enabled", got.TTS.Provider)
	}
}

func TestLoadProcessConfigPCMDownlinkKeepsRealTTS(t *testing.T) {
	cfg, err := loadProcessConfig(func(key string) string {
		switch key {
		case "REALTIME_TICKET_SECRET":
			return strings.Repeat("p", 32)
		case "REALTIME_TTS_DOWNLINK":
			return "pcm"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("loadProcessConfig() error = %v", err)
	}
	if cfg.DownlinkMode != "pcm" || !cfg.SkipTTSTrack || cfg.ForceMockTTS || cfg.DownlinkCodec != "pcm" {
		t.Fatalf("pcm downlink config = %#v", cfg)
	}
}

func setMockProviderEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "test")
	t.Setenv("ASR_PROVIDER", "mock")
	t.Setenv("LLM_PROVIDER", "mock")
	t.Setenv("TTS_PROVIDER", "mock")
	t.Setenv("REALTIME_API_DATABASE", "")
	// Unit tests stay offline: Silero needs ONNX Runtime shared libs.
	t.Setenv("LOCAL_VAD_PROVIDER", "energy")
	t.Setenv("REALTIME_METRICS_TOKEN", "")
	t.Setenv("COMMAND_INTERPRETER", "")
	t.Setenv("COMMAND_LLM_API_KEY", "")
	t.Setenv("COMMAND_LLM_BASE_URL", "")
	t.Setenv("COMMAND_LLM_MODEL", "")
	t.Setenv("COMMAND_LLM_TIMEOUT_MS", "")
	t.Setenv("LINGOW_API_BASE_URL", "")
	t.Setenv("LINGOW_COMMAND_SYSTEM_TOKEN", "")
	t.Setenv("COMMAND_CONFIG_TIMEOUT_MS", "")
}

func TestNewControlPlaneHandlerWiresQwenSemanticCommands(t *testing.T) {
	setMockProviderEnv(t)
	t.Setenv("COMMAND_INTERPRETER", "qwen")
	t.Setenv("COMMAND_LLM_API_KEY", "test-command-key")
	t.Setenv("COMMAND_LLM_BASE_URL", "https://example.invalid/v1")
	t.Setenv("LINGOW_API_BASE_URL", "http://api:8080")
	t.Setenv("LINGOW_COMMAND_SYSTEM_TOKEN", strings.Repeat("c", minCommandTokenBytes))

	handler, err := newControlPlaneHandler(strings.Repeat("s", minTicketSecretBytes))
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}
	if handler == nil {
		t.Fatal("newControlPlaneHandler() = nil")
	}
}

func TestNewControlPlaneHandlerRejectsQwenWithoutCommandAPI(t *testing.T) {
	setMockProviderEnv(t)
	t.Setenv("COMMAND_INTERPRETER", "qwen")
	t.Setenv("COMMAND_LLM_API_KEY", "test-command-key")
	t.Setenv("COMMAND_LLM_BASE_URL", "https://example.invalid/v1")

	_, err := newControlPlaneHandler(strings.Repeat("s", minTicketSecretBytes))
	if err == nil || !strings.Contains(err.Error(), "LINGOW_API_BASE_URL") {
		t.Fatalf("newControlPlaneHandler() error = %v, want missing command API configuration", err)
	}
}

func TestNewControlPlaneHandlerLegacyDoesNotConstructUnusedCommandAPIClient(t *testing.T) {
	setMockProviderEnv(t)
	t.Setenv("COMMAND_INTERPRETER", "legacy")
	t.Setenv("LINGOW_API_BASE_URL", "://invalid-command-api")
	t.Setenv("LINGOW_COMMAND_SYSTEM_TOKEN", strings.Repeat("c", minCommandTokenBytes))

	handler, err := newControlPlaneHandler(strings.Repeat("s", minTicketSecretBytes))
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}
	if handler == nil {
		t.Fatal("newControlPlaneHandler() = nil")
	}
}

func TestNewControlPlaneHandlerServesWebRTCConfig(t *testing.T) {
	setMockProviderEnv(t)
	secret := strings.Repeat("r", 32)
	handler, err := newControlPlaneHandler(secret)
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}

	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte(secret),
		TTL:    time.Minute,
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	ticket, err := codec.Issue("vs_test", "account_test")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/realtime/v1/sessions/vs_test/webrtc/config",
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+ticket)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}

	var body struct {
		SessionID          string `json:"session_id"`
		ControlDataChannel struct {
			Label           string `json:"label"`
			Ordered         bool   `json:"ordered"`
			ProtocolVersion int    `json:"protocol_version"`
		} `json:"control_data_channel"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.SessionID != "vs_test" {
		t.Fatalf("session_id = %q", body.SessionID)
	}
	if body.ControlDataChannel.Label != realtimev1.ControlDataChannelLabel ||
		!body.ControlDataChannel.Ordered ||
		body.ControlDataChannel.ProtocolVersion != realtimev1.ControlProtocolVersion {
		t.Fatalf("control_data_channel = %#v", body.ControlDataChannel)
	}
}

func TestNewControlPlaneHandlerProtectsRealtimeMetrics(t *testing.T) {
	setMockProviderEnv(t)
	t.Setenv("REALTIME_METRICS_TOKEN", "metrics-secret")
	handler, err := newControlPlaneHandler(strings.Repeat("m", 32))
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized metrics status = %d", unauthorized.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("Authorization", "Bearer metrics-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("metrics Content-Type = %q, want application/json", contentType)
	}
	if body := strings.TrimSpace(response.Body.String()); body == "" || !strings.Contains(body, "mode_commands") {
		t.Fatalf("metrics body = %q, want mode_commands snapshot", body)
	}
}

func TestNewControlPlaneHandlerDoesNotExposeMetricsByDefault(t *testing.T) {
	setMockProviderEnv(t)
	handler, err := newControlPlaneHandler(strings.Repeat("m", 32))
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("metrics status = %d, want 404 while disabled", response.Code)
	}
}

func TestNewControlPlaneHandlerRejectsMissingTicket(t *testing.T) {
	setMockProviderEnv(t)
	handler, err := newControlPlaneHandler(strings.Repeat("r", 32))
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/realtime/v1/sessions/vs_test/webrtc/config",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatalf("status = %d, want auth failure", response.Code)
	}
}

func TestNewHTTPServerAppliesTimeouts(t *testing.T) {
	server := newHTTPServer(":8090", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if server.Addr != ":8090" {
		t.Fatalf("Addr = %q", server.Addr)
	}
	if server.ReadHeaderTimeout != httpReadHeaderTimeout ||
		server.ReadTimeout != httpReadTimeout ||
		server.WriteTimeout != httpWriteTimeout ||
		server.IdleTimeout != httpIdleTimeout {
		t.Fatalf("timeouts not applied: %#v", server)
	}
}

func TestNewControlPlaneHandlerRejectsShortSecret(t *testing.T) {
	setMockProviderEnv(t)
	if _, err := newControlPlaneHandler("short"); err == nil {
		t.Fatal("newControlPlaneHandler(short) error = nil")
	}
}

func TestRunRejectsMissingSecretWithoutListening(t *testing.T) {
	err := run(context.Background(), func(string) string { return "" }, nil)
	if err == nil {
		t.Fatal("run() error = nil, want config error")
	}
	if !strings.Contains(err.Error(), "REALTIME_TICKET_SECRET") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunShutsDownAfterStart(t *testing.T) {
	setMockProviderEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, secretEnv(strings.Repeat("u", 32)), func(server *http.Server) error {
			shutdownStarted := make(chan struct{})
			server.RegisterOnShutdown(func() { close(shutdownStarted) })
			close(started)
			<-shutdownStarted
			return http.ErrServerClosed
		})
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("listener did not start")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run() did not return after shutdown")
	}
}

func TestRunReturnsListenError(t *testing.T) {
	setMockProviderEnv(t)
	want := errors.New("listen failed")
	err := run(context.Background(), secretEnv(strings.Repeat("v", 32)), func(*http.Server) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("run() error = %v, want %v", err, want)
	}
}

func TestRunAcceptsListenerExit(t *testing.T) {
	setMockProviderEnv(t)
	err := run(context.Background(), secretEnv(strings.Repeat("w", 32)), func(*http.Server) error {
		return nil
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunAcceptsServerClosedListenerExit(t *testing.T) {
	setMockProviderEnv(t)
	err := run(context.Background(), secretEnv(strings.Repeat("x", 32)), func(*http.Server) error {
		return http.ErrServerClosed
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestAPIDatabaseAndUsageOutboxFlags(t *testing.T) {
	if apiDatabaseEnabled(func(string) string { return "enabled" }) != true {
		t.Fatal("enabled should be true")
	}
	if apiDatabaseEnabled(func(string) string { return "off" }) {
		t.Fatal("off should be false")
	}
	if usageOutboxEnabled(func(string) string { return "valkey" }) != true {
		t.Fatal("valkey should enable usage outbox")
	}
	if usageOutboxEnabled(func(string) string { return "memory" }) {
		t.Fatal("memory should not enable usage outbox")
	}
	if usageOutboxEnabled(nil) {
		t.Fatal("nil getenv without env should be false")
	}
}

func TestLoadProcessConfigOpusAndCustomLanguages(t *testing.T) {
	cfg, err := loadProcessConfig(func(key string) string {
		switch key {
		case "REALTIME_TICKET_SECRET":
			return strings.Repeat("o", 32)
		case "REALTIME_TTS_DOWNLINK":
			return "opus"
		case "REALTIME_SOURCE_LANGUAGE":
			return "en-US"
		case "REALTIME_TARGET_LANGUAGE":
			return "zh-CN"
		case "REALTIME_ADDR":
			return ":19090"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("loadProcessConfig() error = %v", err)
	}
	if cfg.DownlinkMode != "opus" || cfg.SkipTTSTrack || cfg.ForceMockTTS || cfg.DownlinkCodec != "opus" {
		t.Fatalf("opus config = %#v", cfg)
	}
	if cfg.SourceLanguage != "en-US" || cfg.TargetLanguage != "zh-CN" || cfg.Addr != ":19090" {
		t.Fatalf("languages/addr = %#v", cfg)
	}
}

func TestMockOfflineProvidersSwitchWithSourceLanguage(t *testing.T) {
	zh := mockOfflineProviders("zh-CN")
	en := mockOfflineProviders("")
	enUS := mockOfflineProviders("en-US")
	if zh.ASR == nil || en.ASR == nil || enUS.Translation == nil || zh.TTS == nil {
		t.Fatal("providers missing")
	}
	zhStream, err := zh.ASR.StartStream(context.Background(), asr.StreamRequest{SourceLanguage: "zh-CN"})
	if err != nil {
		t.Fatalf("zh StartStream: %v", err)
	}
	zhFinal, err := zhStream.Finish(context.Background())
	if err != nil {
		t.Fatalf("zh Finish: %v", err)
	}
	if zhFinal.Text != "你好" || zhFinal.SourceLanguage != "zh-CN" {
		t.Fatalf("zh final = %#v", zhFinal)
	}
	enStream, err := enUS.ASR.StartStream(context.Background(), asr.StreamRequest{SourceLanguage: "en-US"})
	if err != nil {
		t.Fatalf("en StartStream: %v", err)
	}
	enFinal, err := enStream.Finish(context.Background())
	if err != nil {
		t.Fatalf("en Finish: %v", err)
	}
	if enFinal.Text != "Hello" || enFinal.SourceLanguage != "en-US" {
		t.Fatalf("en final = %#v", enFinal)
	}
	translated, err := enUS.Translation.Translate(context.Background(), translate.Request{
		Text: "Hello", SourceLanguage: "en-US", TargetLanguage: "zh-CN",
	})
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if translated.Text != "你好" {
		t.Fatalf("translated = %#v", translated)
	}
}

func TestNewControlPlaneHandlerPCMDownlinkStarts(t *testing.T) {
	setMockProviderEnv(t)
	t.Setenv("REALTIME_TTS_DOWNLINK", "pcm")
	handler, err := newControlPlaneHandler(strings.Repeat("p", 32))
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}
	if handler == nil {
		t.Fatal("handler = nil")
	}
}

func TestControlPlaneStartWithTrustSession(t *testing.T) {
	setMockProviderEnv(t)
	secret := strings.Repeat("t", 32)
	handler, err := newControlPlaneHandler(secret)
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}
	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte(secret),
		TTL:    time.Minute,
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	ticket, err := codec.Issue("vs_start", "account_start")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	body := strings.NewReader(`{"operation_id":"op-1","trace_id":"trace-1"}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/realtime/v1/sessions/vs_start/start",
		body,
	)
	request.Header.Set("Authorization", "Bearer "+ticket)
	request.Header.Set("Idempotency-Key", "start-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", response.Code, response.Body.String())
	}

	runtimeReq := httptest.NewRequest(
		http.MethodGet,
		"/realtime/v1/sessions/vs_start/runtime",
		nil,
	)
	runtimeReq.Header.Set("Authorization", "Bearer "+ticket)
	runtimeRes := httptest.NewRecorder()
	handler.ServeHTTP(runtimeRes, runtimeReq)
	if runtimeRes.Code != http.StatusOK {
		t.Fatalf("runtime status = %d body = %s", runtimeRes.Code, runtimeRes.Body.String())
	}
}
