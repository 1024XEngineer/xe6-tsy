package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/runtime"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

func TestHandlerStartStopDelegatesAndReplaysIdempotently(t *testing.T) {
	fixture := newFixture(t)
	startBody := `{"operation_id":"operation-1","trace_id":"trace-start","started_by":"browser","initial_mode":"assistant"}`

	first := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", startBody, "start-key")
	if first.Code != http.StatusOK {
		t.Fatalf("first start status = %d, body=%s", first.Code, first.Body.String())
	}
	second := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", startBody, "start-key")
	if second.Code != http.StatusOK {
		t.Fatalf("replayed start status = %d, body=%s", second.Code, second.Body.String())
	}
	if fixture.lifecycle.starts != 1 {
		t.Fatalf("lifecycle starts = %d, want 1", fixture.lifecycle.starts)
	}
	if fixture.lifecycle.startCommand.OperationID != "operation-1" ||
		fixture.lifecycle.startCommand.TraceID != "trace-start" ||
		fixture.lifecycle.startCommand.StartedBy != "browser" ||
		fixture.lifecycle.startCommand.InitialMode != realtimev1.ModeAssistant {
		t.Fatalf("start command = %#v, want complete request mapping", fixture.lifecycle.startCommand)
	}

	stopBody := `{"trace_id":"trace-stop","reason":"user_requested","ended_at":"2023-11-14T22:14:20Z"}`
	firstStop := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/stop", stopBody, "stop-key")
	if firstStop.Code != http.StatusOK {
		t.Fatalf("first stop status = %d, body=%s", firstStop.Code, firstStop.Body.String())
	}
	// End retries may refresh TraceID/EndedAt while reusing stop:<reason>; server
	// hashes reason only so those retries replay instead of conflicting.
	secondStopBody := `{"trace_id":"trace-stop-replay","reason":"user_requested","ended_at":"2023-11-14T22:15:20Z"}`
	secondStop := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/stop", secondStopBody, "stop-key")
	if secondStop.Code != http.StatusOK {
		t.Fatalf("replayed stop status = %d, body=%s", secondStop.Code, secondStop.Body.String())
	}
	if fixture.lifecycle.stops != 1 {
		t.Fatalf("lifecycle stops = %d, want 1", fixture.lifecycle.stops)
	}
	if fixture.lifecycle.stopCommand.TraceID != "trace-stop" ||
		fixture.lifecycle.stopCommand.Reason != "user_requested" ||
		!fixture.lifecycle.stopCommand.EndedAt.Equal(time.Unix(1700000060, 0).UTC()) {
		t.Fatalf("stop command = %#v, want first request mapping", fixture.lifecycle.stopCommand)
	}
}

func TestHandlerAcceptsFallbackPlaybackIdempotently(t *testing.T) {
	fixture := newFixture(t)
	fallback := &fallbackPlaybackFake{}
	fixture.controlHandler.fallback = fallback
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`

	first := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	second := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("fallback statuses = %d, %d", first.Code, second.Code)
	}
	if fallback.calls != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallback.calls)
	}
	var firstReceipt, secondReceipt realtimev1.FallbackPlaybackReceipt
	if err := json.NewDecoder(first.Body).Decode(&firstReceipt); err != nil {
		t.Fatalf("decode first receipt: %v", err)
	}
	if err := json.NewDecoder(second.Body).Decode(&secondReceipt); err != nil {
		t.Fatalf("decode second receipt: %v", err)
	}
	if firstReceipt.Status != realtimev1.FallbackPlaybackAccepted || secondReceipt.Status != realtimev1.FallbackPlaybackAlreadyAccepted {
		t.Fatalf("fallback receipts = %#v, %#v", firstReceipt, secondReceipt)
	}
}

func TestHandlerKeepsFallbackPlaybackIdempotentAcrossInstances(t *testing.T) {
	store := &fallbackReplayStoreFake{accepted: make(map[string]string)}
	firstFixture := newFixture(t)
	firstFallback := &fallbackPlaybackFake{}
	firstFixture.controlHandler.fallback = firstFallback
	firstFixture.controlHandler.fallbackReplays = store
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`

	first := firstFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	secondFixture := newFixture(t)
	secondFallback := &fallbackPlaybackFake{}
	secondFixture.controlHandler.fallback = secondFallback
	secondFixture.controlHandler.fallbackReplays = store
	second := secondFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")

	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("fallback statuses = %d, %d", first.Code, second.Code)
	}
	if firstFallback.calls != 1 || secondFallback.calls != 0 {
		t.Fatalf("fallback calls = %d, %d, want 1, 0", firstFallback.calls, secondFallback.calls)
	}
	var receipt realtimev1.FallbackPlaybackReceipt
	if err := json.NewDecoder(second.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode replay receipt: %v", err)
	}
	if receipt.Status != realtimev1.FallbackPlaybackAlreadyAccepted {
		t.Fatalf("replay receipt = %#v", receipt)
	}
}

func TestHandlerClaimsFallbackBeforeConcurrentCrossInstancePlayback(t *testing.T) {
	store := &fallbackReplayStoreFake{accepted: make(map[string]string)}
	firstFixture := newFixture(t)
	firstFallback := &blockingFallbackPlaybackFake{entered: make(chan struct{}), release: make(chan struct{})}
	firstFixture.controlHandler.fallback = firstFallback
	firstFixture.controlHandler.fallbackReplays = store
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- firstFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	}()
	<-firstFallback.entered

	secondFixture := newFixture(t)
	secondFallback := &fallbackPlaybackFake{}
	secondFixture.controlHandler.fallback = secondFallback
	secondFixture.controlHandler.fallbackReplays = store
	second := secondFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if second.Code != http.StatusConflict {
		t.Fatalf("concurrent replay status = %d, body=%s", second.Code, second.Body.String())
	}
	if secondFallback.calls != 0 {
		t.Fatalf("second fallback calls = %d, want 0", secondFallback.calls)
	}

	close(firstFallback.release)
	if first := <-firstDone; first.Code != http.StatusAccepted {
		t.Fatalf("first fallback status = %d, body=%s", first.Code, first.Body.String())
	}
	if firstFallback.calls.Load() != 1 {
		t.Fatalf("first fallback calls = %d, want 1", firstFallback.calls.Load())
	}
}

func TestHandlerRenewsActiveFallbackClaim(t *testing.T) {
	store := &fallbackReplayStoreFake{accepted: make(map[string]string), renewed: make(chan struct{})}
	fixture := newFixture(t)
	fixture.controlHandler.fallbackClaimRenewInterval = 10 * time.Millisecond
	firstFallback := &blockingFallbackPlaybackFake{entered: make(chan struct{}), release: make(chan struct{})}
	fixture.controlHandler.fallback = firstFallback
	fixture.controlHandler.fallbackReplays = store
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	}()
	<-firstFallback.entered
	select {
	case <-store.renewed:
	case <-time.After(time.Second):
		t.Fatal("fallback claim was not renewed while playback was active")
	}
	secondFixture := newFixture(t)
	secondFixture.controlHandler.fallback = &fallbackPlaybackFake{}
	secondFixture.controlHandler.fallbackReplays = store
	second := secondFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if second.Code != http.StatusConflict {
		t.Fatalf("active replay status = %d, body=%s; want conflict", second.Code, second.Body.String())
	}
	close(firstFallback.release)
	if first := <-done; first.Code != http.StatusAccepted {
		t.Fatalf("first fallback status = %d, body=%s", first.Code, first.Body.String())
	}
}

func TestHandlerStopsFallbackWhenClaimRenewalFails(t *testing.T) {
	store := &fallbackReplayStoreFake{
		accepted: make(map[string]string),
		renewed:  make(chan struct{}),
		renewErr: errors.New("renew unavailable"),
	}
	fixture := newFixture(t)
	fixture.controlHandler.fallbackClaimRenewInterval = 10 * time.Millisecond
	firstFallback := &blockingFallbackPlaybackFake{entered: make(chan struct{}), release: make(chan struct{})}
	fixture.controlHandler.fallback = firstFallback
	fixture.controlHandler.fallbackReplays = store
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	}()
	<-firstFallback.entered
	select {
	case <-store.renewed:
	case <-time.After(time.Second):
		t.Fatal("fallback claim renewal was not attempted")
	}
	select {
	case first := <-done:
		if first.Code != http.StatusInternalServerError {
			t.Fatalf("renewal failure status = %d, body=%s", first.Code, first.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("fallback did not stop after claim renewal failure")
	}
	secondFixture := newFixture(t)
	secondFixture.controlHandler.fallback = &fallbackPlaybackFake{}
	secondFixture.controlHandler.fallbackReplays = store
	second := secondFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if second.Code != http.StatusConflict {
		t.Fatalf("retry after renewal failure status = %d, body=%s; want conflict", second.Code, second.Body.String())
	}
}

func TestHandlerCompletesFallbackWhenStoppingInFlightRenewal(t *testing.T) {
	baseStore := &fallbackReplayStoreFake{accepted: make(map[string]string)}
	store := &blockingRenewFallbackStore{
		fallbackReplayStoreFake: baseStore,
		renewEntered:            make(chan struct{}),
	}
	fixture := newFixture(t)
	fixture.controlHandler.fallbackClaimRenewInterval = 10 * time.Millisecond
	fixture.controlHandler.fallbackReplays = store
	fixture.controlHandler.fallback = &blockingFallbackPlaybackFake{
		entered: make(chan struct{}),
		release: store.renewEntered,
	}
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`

	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	}()

	select {
	case <-store.renewEntered:
	case <-time.After(time.Second):
		t.Fatal("fallback claim renewal did not start")
	}
	var first *httptest.ResponseRecorder
	select {
	case first = <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("fallback request did not finish after heartbeat stopped")
	}
	if first.Code != http.StatusAccepted {
		t.Fatalf("fallback status = %d, body=%s; want accepted", first.Code, first.Body.String())
	}

	secondFixture := newFixture(t)
	secondFallback := &fallbackPlaybackFake{}
	secondFixture.controlHandler.fallback = secondFallback
	secondFixture.controlHandler.fallbackReplays = store
	second := secondFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if second.Code != http.StatusAccepted {
		t.Fatalf("replayed fallback status = %d, body=%s; want accepted", second.Code, second.Body.String())
	}
	var receipt realtimev1.FallbackPlaybackReceipt
	if err := json.NewDecoder(second.Body).Decode(&receipt); err != nil {
		t.Fatalf("decode replay receipt: %v", err)
	}
	if receipt.Status != realtimev1.FallbackPlaybackAlreadyAccepted {
		t.Fatalf("replay receipt = %#v, want already accepted", receipt)
	}
	if secondFallback.calls != 0 {
		t.Fatalf("replayed fallback calls = %d, want 0", secondFallback.calls)
	}
}

func TestHandlerRetriesFallbackAfterExpiredProcessingClaim(t *testing.T) {
	store := &fallbackReplayStoreFake{accepted: make(map[string]string)}
	firstFixture := newFixture(t)
	firstFixture.controlHandler.fallback = &fallbackPlaybackFake{err: errors.New("playback outcome unknown")}
	firstFixture.controlHandler.fallbackReplays = store
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`
	first := firstFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("failed fallback status = %d, body=%s", first.Code, first.Body.String())
	}

	secondFixture := newFixture(t)
	secondFallback := &fallbackPlaybackFake{}
	secondFixture.controlHandler.fallback = secondFallback
	secondFixture.controlHandler.fallbackReplays = store
	store.reconcileProcessing = true
	second := secondFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if second.Code != http.StatusAccepted {
		t.Fatalf("ambiguous replay status = %d, body=%s", second.Code, second.Body.String())
	}
	if secondFallback.calls != 1 {
		t.Fatalf("fallback replay calls = %d, want 1", secondFallback.calls)
	}
}

func TestHandlerReleasesFallbackClaimWhenPlaybackDidNotStart(t *testing.T) {
	store := &fallbackReplayStoreFake{accepted: make(map[string]string)}
	firstFixture := newFixture(t)
	firstFixture.controlHandler.fallback = &fallbackPlaybackFake{err: fallbackPlaybackNotStartedTestError{err: errors.New("runtime unavailable")}}
	firstFixture.controlHandler.fallbackReplays = store
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`

	first := firstFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("failed fallback status = %d, body=%s", first.Code, first.Body.String())
	}
	if store.aborted != 1 {
		t.Fatalf("abort calls = %d, want 1", store.aborted)
	}

	retryFallback := &fallbackPlaybackFake{}
	secondFixture := newFixture(t)
	secondFixture.controlHandler.fallback = retryFallback
	secondFixture.controlHandler.fallbackReplays = store
	second := secondFixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if second.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d, body=%s", second.Code, second.Body.String())
	}
	if retryFallback.calls != 1 {
		t.Fatalf("retry fallback calls = %d, want 1", retryFallback.calls)
	}
}

func TestHandlerKeepsFallbackClaimWhenAbortFails(t *testing.T) {
	store := &fallbackReplayStoreFake{
		accepted: make(map[string]string),
		abortErr: errors.New("abort unavailable"),
	}
	fixture := newFixture(t)
	fixture.controlHandler.fallback = &fallbackPlaybackFake{err: fallbackPlaybackNotStartedTestError{err: errors.New("runtime unavailable")}}
	fixture.controlHandler.fallbackReplays = store
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`

	first := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("failed fallback status = %d, body=%s", first.Code, first.Body.String())
	}
	store.abortErr = nil
	second := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if second.Code != http.StatusConflict {
		t.Fatalf("retry status = %d, body=%s; want conflict", second.Code, second.Body.String())
	}
}

func TestHandlerRejectsInvalidFallbackRequests(t *testing.T) {
	fixture := newFixture(t)
	fixture.controlHandler.fallback = &fallbackPlaybackFake{}
	validBody := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`
	tests := []struct {
		name           string
		body           string
		idempotencyKey string
	}{
		{name: "missing idempotency key", body: validBody},
		{name: "missing trace", body: `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3}`, idempotencyKey: "fallback:fallback-1"},
		{name: "session mismatch", body: `{"operation_id":"fallback-1","session_id":"other-session","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`, idempotencyKey: "fallback:fallback-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", tt.body, tt.idempotencyKey)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("fallback status = %d, body=%s; want bad request", response.Code, response.Body.String())
			}
		})
	}
}

func TestHandlerReturnsCompletionFailureAndPayloadConflict(t *testing.T) {
	store := &fallbackReplayStoreFake{accepted: make(map[string]string), completeErr: errors.New("replay store unavailable")}
	fixture := newFixture(t)
	fixture.controlHandler.fallback = &fallbackPlaybackFake{}
	fixture.controlHandler.fallbackReplays = store
	body := `{"operation_id":"fallback-1","session_id":"session-1","turn_id":"turn-1","target_language":"zh-CN","translated_text":"translated","language_config_version":3,"trace_id":"trace-1"}`
	failed := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("completion failure status = %d, body=%s; want internal error", failed.Code, failed.Body.String())
	}

	store.completeErr = nil
	store.reconcileProcessing = true
	accepted := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", body, "fallback:fallback-1")
	if accepted.Code != http.StatusAccepted {
		t.Fatalf("retry status = %d, body=%s; want accepted", accepted.Code, accepted.Body.String())
	}
	conflictBody := strings.Replace(body, "translated", "changed", 1)
	conflict := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/fallback-playback", conflictBody, "fallback:fallback-1")
	if conflict.Code != http.StatusBadRequest {
		t.Fatalf("payload conflict status = %d, body=%s; want bad request", conflict.Code, conflict.Body.String())
	}
}

func TestHandlerDelegatesOfferCandidatesRuntimeAndConfig(t *testing.T) {
	fixture := newFixture(t)

	runtime := fixture.request(http.MethodGet, "/realtime/v1/sessions/session-1/runtime", "", "")
	if runtime.Code != http.StatusOK {
		t.Fatalf("runtime status = %d, body=%s", runtime.Code, runtime.Body.String())
	}
	config := fixture.request(http.MethodGet, "/realtime/v1/sessions/session-1/webrtc/config", "", "")
	if config.Code != http.StatusOK {
		t.Fatalf("config status = %d, body=%s", config.Code, config.Body.String())
	}
	if !strings.Contains(config.Body.String(), `"session_id":"session-1"`) {
		t.Fatalf("config body = %s", config.Body.String())
	}

	offer := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/webrtc/offer", `{"sdp":"offer-sdp","type":"offer"}`, "offer-key")
	if offer.Code != http.StatusOK {
		t.Fatalf("offer status = %d, body=%s", offer.Code, offer.Body.String())
	}
	var offerResponse webrtc.OfferResponse
	if err := json.Unmarshal(offer.Body.Bytes(), &offerResponse); err != nil {
		t.Fatalf("decode offer response: %v", err)
	}
	if offerResponse.ConnectionID != "connection-1" {
		t.Fatalf("connection id = %q, want connection-1", offerResponse.ConnectionID)
	}

	candidates := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/ice-candidates", `{"connection_id":"connection-1","candidates":[{"candidate_id":"candidate-1","candidate":"candidate:1"}],"end_of_candidates":true}`, "")
	if candidates.Code != http.StatusOK {
		t.Fatalf("candidates status = %d, body=%s", candidates.Code, candidates.Body.String())
	}
	if fixture.signaling.offerCalls != 1 || fixture.signaling.candidateCalls != 1 {
		t.Fatalf("signaling calls = offer %d, candidates %d", fixture.signaling.offerCalls, fixture.signaling.candidateCalls)
	}
}

func TestHandlerReturnsAllConnectionStates(t *testing.T) {
	for _, state := range []realtimev1.ConnectionState{
		realtimev1.ConnectionNew,
		realtimev1.ConnectionConnecting,
		realtimev1.ConnectionConnected,
		realtimev1.ConnectionDisconnected,
		realtimev1.ConnectionFailed,
		realtimev1.ConnectionClosed,
	} {
		t.Run(string(state), func(t *testing.T) {
			fixture := newFixture(t)
			fixture.connections.snapshot.State = state
			response := fixture.request(
				http.MethodGet,
				"/realtime/v1/sessions/session-1/connection",
				"",
				"",
			)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
			}
			var snapshot realtimev1.ConnectionSnapshot
			if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if snapshot.SessionID != "session-1" ||
				snapshot.ConnectionID != "connection-1" ||
				snapshot.State != state ||
				snapshot.UpdatedAt.IsZero() {
				t.Fatalf("snapshot = %#v", snapshot)
			}
		})
	}
}

func TestMapErrorTTSCodecUnsupported(t *testing.T) {
	status, code := mapError(webrtc.ErrTTSCodecUnsupported)
	if status != http.StatusBadRequest || code != "tts_codec_unsupported" {
		t.Fatalf("mapError(ErrTTSCodecUnsupported) = %d %q", status, code)
	}
}

func TestMapErrorTreatsModeEventFailureAsUnavailable(t *testing.T) {
	status, code := mapError(runtime.ErrModeEventUnavailable)
	if status != http.StatusServiceUnavailable || code != "service_unavailable" {
		t.Fatalf("mapError(ErrModeEventUnavailable) = %d %q", status, code)
	}
}

func TestMapErrorKeepsLegacyRuntimeNotFoundOutsideModeControl(t *testing.T) {
	status, code := mapError(session.ErrRuntimeNotFound)
	if status != http.StatusNotFound || code != "not_found" {
		t.Fatalf("mapError(ErrRuntimeNotFound) = %d %q", status, code)
	}
}

func TestHandlerMapsConnectionReaderErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		status   int
		wantCode string
	}{
		{
			name:     "not found",
			err:      webrtc.ErrConnectionNotFound,
			status:   http.StatusNotFound,
			wantCode: string(realtimev1.ErrorConnectionNotFound),
		},
		{
			name:     "provider",
			err:      errors.New("provider unavailable"),
			status:   http.StatusInternalServerError,
			wantCode: "internal_error",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.connections.err = test.err
			response := fixture.request(
				http.MethodGet,
				"/realtime/v1/sessions/session-1/connection",
				"",
				"",
			)
			if response.Code != test.status {
				t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
			}
			var payload errorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if payload.Error.Code != test.wantCode {
				t.Fatalf("error.code = %q, want %q", payload.Error.Code, test.wantCode)
			}
		})
	}
}

func TestHandlerRejectsMalformedAndMissingIdentity(t *testing.T) {
	fixture := newFixture(t)

	missingTicket := httptest.NewRecorder()
	fixture.handler.ServeHTTP(missingTicket, httptest.NewRequest(http.MethodGet, "/realtime/v1/sessions/session-1/runtime", nil))
	if missingTicket.Code != http.StatusUnauthorized {
		t.Fatalf("missing ticket status = %d, want 401", missingTicket.Code)
	}

	malformed := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{"trace_id":`, "start-key")
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed status = %d, want 400", malformed.Code)
	}

	unknown := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{"trace_id":"x","unknown":true}`, "start-key")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", unknown.Code)
	}

	missingOperation := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{}`, "start-key")
	if missingOperation.Code != http.StatusBadRequest {
		t.Fatalf("missing operation status = %d, want 400", missingOperation.Code)
	}
	if fixture.lifecycle.starts != 0 {
		t.Fatalf("lifecycle starts = %d, want 0", fixture.lifecycle.starts)
	}

	invalidStop := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/stop", `{"reason":"unknown"}`, "stop-key")
	if invalidStop.Code != http.StatusBadRequest {
		t.Fatalf("invalid stop status = %d, want 400", invalidStop.Code)
	}
	if fixture.lifecycle.stops != 0 {
		t.Fatalf("lifecycle stops = %d, want 0", fixture.lifecycle.stops)
	}

	missingKey := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/webrtc/offer", `{"sdp":"offer-sdp","type":"offer"}`, "")
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency status = %d, want 400", missingKey.Code)
	}
}

func TestHandlerMapsLifecycleAndTicketErrors(t *testing.T) {
	fixture := newFixture(t)
	fixture.lifecycle.startErr = session.ErrRuntimeCleanupRequired
	conflict := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{"operation_id":"operation-1"}`, "start-key")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("lifecycle conflict status = %d, body=%s", conflict.Code, conflict.Body.String())
	}

	fixture.tickets.err = webrtc.ErrTicketExpired
	expired := fixture.request(http.MethodGet, "/realtime/v1/sessions/session-1/runtime", "", "")
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("expired ticket status = %d, want 401", expired.Code)
	}
}

func TestHandlerMapsRuntimeOperationConflictCode(t *testing.T) {
	fixture := newFixture(t)
	fixture.lifecycle.startErr = session.ErrRuntimeOperationConflict
	response := fixture.request(
		http.MethodPost,
		"/realtime/v1/sessions/session-1/start",
		`{"operation_id":"operation-1"}`,
		"start-key",
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var payload errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := payload.Error.Code; got != string(realtimev1.ErrorRuntimeOperationConflict) {
		t.Fatalf("error.code = %q", got)
	}
}

func TestHandlerReservesReplayBeforeRunningLifecycle(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &blockingLifecycleFake{
		runtime: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeListening, UpdatedAt: now},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	handler := newReplayHandler(t, lifecycle, func() time.Time { return now }, time.Minute, 8)
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- replayRequest(handler, `{"operation_id":"operation-1","trace_id":"trace-start","started_by":"browser"}`, "start-key")
	}()
	<-lifecycle.entered

	conflict := replayRequest(handler, `{"operation_id":"operation-1","trace_id":"different","started_by":"browser"}`, "start-key")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("in-flight payload conflict status = %d, body=%s", conflict.Code, conflict.Body.String())
	}
	close(lifecycle.release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first start status = %d, body=%s", first.Code, first.Body.String())
	}
	if got := lifecycle.starts.Load(); got != 1 {
		t.Fatalf("lifecycle starts = %d, want 1", got)
	}
}

func TestHandlerExpiresReplayRecords(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &lifecycleFake{runtime: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeListening, UpdatedAt: now}}
	handler := newReplayHandler(t, lifecycle, func() time.Time { return now }, time.Minute, 8)
	body := `{"operation_id":"operation-1","trace_id":"trace-start","started_by":"browser"}`
	if response := replayRequest(handler, body, "start-key"); response.Code != http.StatusOK {
		t.Fatalf("first start status = %d, body=%s", response.Code, response.Body.String())
	}
	now = now.Add(time.Minute + time.Second)
	if response := replayRequest(handler, body, "start-key"); response.Code != http.StatusOK {
		t.Fatalf("expired replay status = %d, body=%s", response.Code, response.Body.String())
	}
	if lifecycle.starts != 2 {
		t.Fatalf("lifecycle starts after expiry = %d, want 2", lifecycle.starts)
	}
}

func TestHandlerRejectsReplayWhenCapacityIsExhausted(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &lifecycleFake{runtime: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeListening, UpdatedAt: now}}
	handler := newReplayHandler(t, lifecycle, func() time.Time { return now }, time.Minute, 1)
	body := `{"operation_id":"operation-1","trace_id":"trace-start","started_by":"browser"}`
	if response := replayRequest(handler, body, "start-key-1"); response.Code != http.StatusOK {
		t.Fatalf("first start status = %d, body=%s", response.Code, response.Body.String())
	}
	full := replayRequest(handler, body, "start-key-2")
	if full.Code != http.StatusServiceUnavailable {
		t.Fatalf("full replay status = %d, body=%s", full.Code, full.Body.String())
	}
	now = now.Add(time.Minute + time.Second)
	if response := replayRequest(handler, body, "start-key-2"); response.Code != http.StatusOK {
		t.Fatalf("recovered replay status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerIsolatesReplayCapacityPerSession(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &multiSessionLifecycleFake{runtime: session.RuntimeSnapshot{RuntimeState: session.RuntimeListening, UpdatedAt: now}}
	handler := newReplayHandlerWithLimits(t, lifecycle, func() time.Time { return now }, time.Minute, 2, 1)
	body := `{"operation_id":"operation-1"}`
	if response := replayRequestForSession(handler, "session-1", body, "session-1-key"); response.Code != http.StatusOK {
		t.Fatalf("first session start status = %d, body=%s", response.Code, response.Body.String())
	}
	if response := replayRequestForSession(handler, "session-1", body, "session-1-key-2"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("second session-1 key status = %d, body=%s", response.Code, response.Body.String())
	}
	if response := replayRequestForSession(handler, "session-2", body, "session-2-key"); response.Code != http.StatusOK {
		t.Fatalf("session-2 start status = %d, body=%s", response.Code, response.Body.String())
	}
	if lifecycle.starts != 2 {
		t.Fatalf("lifecycle starts = %d, want 2", lifecycle.starts)
	}
}

func TestHandlerRejectsOversizedIdempotencyKey(t *testing.T) {
	fixture := newFixture(t)
	key := strings.Repeat("k", maxIdempotencyKeyBytes+1)
	response := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{}`, key)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized idempotency key status = %d, body=%s", response.Code, response.Body.String())
	}
	if fixture.lifecycle.starts != 0 {
		t.Fatalf("lifecycle starts = %d, want 0", fixture.lifecycle.starts)
	}
}

func TestHandlerRejectsBodyBeyondLimitIncludingTrailingWhitespace(t *testing.T) {
	fixture := newFixture(t)
	body := `{}` + strings.Repeat(" ", maxBodyBytes)
	response := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", body, "body-limit-key")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d, body=%s", response.Code, response.Body.String())
	}
	if fixture.lifecycle.starts != 0 {
		t.Fatalf("lifecycle starts = %d, want 0", fixture.lifecycle.starts)
	}
}

func TestHandlerMapsMissingConnectionIDToBadRequest(t *testing.T) {
	fixture := newFixture(t)
	response := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/ice-candidates", `{"candidates":[]}`, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing connection id status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerDoesNotCacheFailedReplay(t *testing.T) {
	fixture := newFixture(t)
	fixture.lifecycle.startErr = errors.New("temporary lifecycle failure")
	failed := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{"operation_id":"operation-1"}`, "start-key")
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("failed start status = %d, body=%s", failed.Code, failed.Body.String())
	}
	fixture.lifecycle.startErr = nil
	recovered := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{"operation_id":"operation-1"}`, "start-key")
	if recovered.Code != http.StatusOK {
		t.Fatalf("recovered start status = %d, body=%s", recovered.Code, recovered.Body.String())
	}
	if fixture.lifecycle.starts != 2 {
		t.Fatalf("lifecycle starts after recovery = %d, want 2", fixture.lifecycle.starts)
	}
}

func TestHandlerRejectsWrongSessionTicketAndAcceptsRepeatedCandidate(t *testing.T) {
	fixture := newFixture(t)
	fixture.tickets.ticket.SessionID = "another-session"
	wrongSession := fixture.request(http.MethodGet, "/realtime/v1/sessions/session-1/runtime", "", "")
	if wrongSession.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-session ticket status = %d, want 401", wrongSession.Code)
	}

	fixture.tickets.ticket.SessionID = "session-1"
	body := `{"connection_id":"connection-1","candidates":[],"end_of_candidates":true}`
	first := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/ice-candidates", body, "")
	second := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/ice-candidates", body, "")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("repeated candidate statuses = %d, %d", first.Code, second.Code)
	}
}

type fixture struct {
	handler        http.Handler
	controlHandler *Handler
	lifecycle      *lifecycleFake
	modes          *modeControlFake
	signaling      *signalingFake
	connections    *connectionFake
	tickets        *ticketFake
	config         *configFake
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &lifecycleFake{
		runtime: session.RuntimeSnapshot{
			SessionID: "session-1", StartOperationID: "operation-1",
			RuntimeState: session.RuntimeListening, UpdatedAt: now,
		},
		stopped: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeStopped, UpdatedAt: now},
	}
	tickets := &ticketFake{ticket: webrtc.ConnectionTicket{SessionID: "session-1", AccountID: "account-1", ExpiresAt: now.Add(time.Hour)}}
	config := &configFake{value: WebRTCConfig{SessionID: "session-1", ExpiresAt: now.Add(time.Hour), ICETransportPolicy: "all"}}
	connections := &connectionFake{snapshot: realtimev1.ConnectionSnapshot{
		SessionID: "session-1", ConnectionID: "connection-1",
		State: realtimev1.ConnectionConnected, Version: 1, UpdatedAt: now,
	}}
	modes := &modeControlFake{state: realtimev1.ModeStateSnapshot{
		SessionID: "session-1", RuntimeInstanceID: "runtime-1", ActiveMode: realtimev1.ModeInterpretation,
		Generation: 1, Phase: realtimev1.ModePhaseActive, UpdatedAt: now,
	}}
	handler, err := New(Dependencies{
		Lifecycle: lifecycle, Modes: modes, Signaling: &signalingFake{}, Connections: connections,
		Tickets: tickets, Config: config, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return fixture{
		handler: handler, controlHandler: handler, lifecycle: lifecycle, modes: modes,
		signaling: handler.signaling.(*signalingFake), connections: connections,
		tickets: tickets, config: config,
	}
}

func newReplayHandler(t *testing.T, lifecycle Lifecycle, now func() time.Time, replayTTL time.Duration, replayMax int) *Handler {
	return newReplayHandlerWithLimits(t, lifecycle, now, replayTTL, replayMax, 0)
}

func newReplayHandlerWithLimits(t *testing.T, lifecycle Lifecycle, now func() time.Time, replayTTL time.Duration, replayMax, replayMaxPerSession int) *Handler {
	t.Helper()
	baseNow := time.Unix(1700000000, 0).UTC()
	tickets := &sessionTicketFake{expiresAt: baseNow.Add(time.Hour)}
	config := &configFake{value: WebRTCConfig{SessionID: "session-1", ExpiresAt: baseNow.Add(time.Hour), ICETransportPolicy: "all"}}
	handler, err := New(Dependencies{
		Lifecycle: lifecycle, Modes: &modeControlFake{}, Signaling: &signalingFake{}, Connections: &connectionFake{},
		Tickets: tickets, Config: config, Now: now,
		ReplayTTL: replayTTL, ReplayMaxEntries: replayMax, ReplayMaxEntriesPerSession: replayMaxPerSession,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return handler
}

func replayRequest(handler http.Handler, body, idempotencyKey string) *httptest.ResponseRecorder {
	return replayRequestForSession(handler, "session-1", body, idempotencyKey)
}

func replayRequestForSession(handler http.Handler, sessionID, body, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/realtime/v1/sessions/"+sessionID+"/start", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer realtime-ticket")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func (f fixture) request(method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer realtime-ticket")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

type lifecycleFake struct {
	runtime      session.RuntimeSnapshot
	stopped      session.RuntimeSnapshot
	startErr     error
	stopErr      error
	runtimeErr   error
	starts       int
	stops        int
	startCommand session.StartRealtimeCommand
	stopCommand  session.StopRealtimeCommand
}

type blockingLifecycleFake struct {
	runtime session.RuntimeSnapshot
	entered chan struct{}
	release chan struct{}
	starts  atomic.Int32
}

func (f *blockingLifecycleFake) Start(ctx context.Context, _ session.StartRealtimeCommand) (session.RuntimeSnapshot, error) {
	if f.starts.Add(1) == 1 {
		close(f.entered)
		select {
		case <-f.release:
		case <-ctx.Done():
			return session.RuntimeSnapshot{}, ctx.Err()
		}
	}
	return f.runtime, nil
}

func (f *blockingLifecycleFake) Stop(context.Context, session.StopRealtimeCommand) (session.RuntimeSnapshot, error) {
	return f.runtime, nil
}

func (f *blockingLifecycleFake) GetRuntimeState(context.Context, string) (session.RuntimeSnapshot, error) {
	return f.runtime, nil
}

func (f *lifecycleFake) Start(_ context.Context, command session.StartRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.starts++
	f.startCommand = command
	if f.startErr != nil {
		return session.RuntimeSnapshot{}, f.startErr
	}
	if command.SessionID != "session-1" {
		return session.RuntimeSnapshot{}, errors.New("unexpected session")
	}
	return f.runtime, nil
}

type multiSessionLifecycleFake struct {
	runtime session.RuntimeSnapshot
	starts  int
}

func (f *multiSessionLifecycleFake) Start(_ context.Context, command session.StartRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.starts++
	f.runtime.SessionID = command.SessionID
	return f.runtime, nil
}

func (f *multiSessionLifecycleFake) Stop(_ context.Context, command session.StopRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.runtime.SessionID = command.SessionID
	return f.runtime, nil
}

func (f *multiSessionLifecycleFake) GetRuntimeState(context.Context, string) (session.RuntimeSnapshot, error) {
	return f.runtime, nil
}

func (f *lifecycleFake) Stop(_ context.Context, command session.StopRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.stops++
	f.stopCommand = command
	if f.stopErr != nil {
		return session.RuntimeSnapshot{}, f.stopErr
	}
	if command.SessionID != "session-1" {
		return session.RuntimeSnapshot{}, errors.New("unexpected session")
	}
	return f.stopped, nil
}

func (f *lifecycleFake) GetRuntimeState(context.Context, string) (session.RuntimeSnapshot, error) {
	if f.runtimeErr != nil {
		return session.RuntimeSnapshot{}, f.runtimeErr
	}
	return f.runtime, nil
}

type signalingFake struct {
	offerCalls     int
	candidateCalls int
}

type fallbackPlaybackFake struct {
	calls int
	err   error
}

type fallbackReplayStoreFake struct {
	mu                  sync.Mutex
	accepted            map[string]string
	processing          map[string]bool
	tokens              map[string]string
	reconcileProcessing bool
	renewErr            error
	renewed             chan struct{}
	renewCount          int
	completeErr         error
	abortErr            error
	aborted             int
}

type blockingRenewFallbackStore struct {
	*fallbackReplayStoreFake
	renewEntered chan struct{}
}

func (s *blockingRenewFallbackStore) Renew(ctx context.Context, _ string, _ string, _ string, _ string) error {
	close(s.renewEntered)
	<-ctx.Done()
	return ctx.Err()
}

func (s *fallbackReplayStoreFake) Claim(_ context.Context, sessionID, operationID, payloadHash string) (FallbackPlaybackClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sessionID + "\x00" + operationID
	storedHash, ok := s.accepted[key]
	if !ok {
		s.accepted[key] = payloadHash
		if s.processing == nil {
			s.processing = make(map[string]bool)
		}
		if s.tokens == nil {
			s.tokens = make(map[string]string)
		}
		s.processing[key] = true
		s.tokens[key] = "token-" + key
		return FallbackPlaybackClaim{Status: FallbackPlaybackClaimed, Token: s.tokens[key]}, nil
	}
	if storedHash != payloadHash {
		return FallbackPlaybackClaim{}, webrtc.ErrIdempotencyPayloadConflict
	}
	if s.processing[key] {
		if s.reconcileProcessing {
			if s.tokens == nil {
				s.tokens = make(map[string]string)
			}
			s.tokens[key] = "reclaimed-" + key
			return FallbackPlaybackClaim{Status: FallbackPlaybackClaimed, Token: s.tokens[key]}, nil
		}
		return FallbackPlaybackClaim{Status: FallbackPlaybackProcessing}, nil
	}
	return FallbackPlaybackClaim{Status: FallbackPlaybackAccepted}, nil
}

func (s *fallbackReplayStoreFake) Renew(_ context.Context, sessionID, operationID, payloadHash, claimToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.renewCount++
	if s.renewCount == 1 && s.renewed != nil {
		close(s.renewed)
	}
	if s.renewErr != nil {
		return s.renewErr
	}
	key := sessionID + "\x00" + operationID
	storedHash, ok := s.accepted[key]
	if !ok || storedHash != payloadHash || !s.processing[key] || s.tokens[key] != claimToken {
		return errors.New("claim is no longer owned")
	}
	return nil
}

func (s *fallbackReplayStoreFake) Complete(_ context.Context, sessionID, operationID, payloadHash, claimToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completeErr != nil {
		return s.completeErr
	}
	key := sessionID + "\x00" + operationID
	if storedHash, ok := s.accepted[key]; !ok || storedHash != payloadHash {
		return webrtc.ErrIdempotencyPayloadConflict
	}
	if s.processing[key] && s.tokens[key] != claimToken {
		return errors.New("claim is no longer owned")
	}
	if s.processing == nil {
		s.processing = make(map[string]bool)
	}
	s.processing[key] = false
	delete(s.tokens, key)
	return nil
}

func (s *fallbackReplayStoreFake) Abort(_ context.Context, sessionID, operationID, payloadHash, claimToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.abortErr != nil {
		return s.abortErr
	}
	key := sessionID + "\x00" + operationID
	storedHash, ok := s.accepted[key]
	if !ok {
		return nil
	}
	if storedHash != payloadHash {
		return webrtc.ErrIdempotencyPayloadConflict
	}
	if !s.processing[key] {
		return nil
	}
	if s.tokens[key] != claimToken {
		return errors.New("claim is no longer owned")
	}
	delete(s.accepted, key)
	delete(s.processing, key)
	delete(s.tokens, key)
	s.aborted++
	return nil
}

func (f *fallbackPlaybackFake) PlayFallback(_ context.Context, _ realtimev1.FallbackPlaybackRequest) error {
	f.calls++
	return f.err
}

type fallbackPlaybackNotStartedTestError struct {
	err error
}

func (e fallbackPlaybackNotStartedTestError) Error() string             { return e.err.Error() }
func (e fallbackPlaybackNotStartedTestError) Unwrap() error             { return e.err }
func (fallbackPlaybackNotStartedTestError) FallbackPlaybackNotStarted() {}

type blockingFallbackPlaybackFake struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (f *blockingFallbackPlaybackFake) PlayFallback(ctx context.Context, _ realtimev1.FallbackPlaybackRequest) error {
	if f.calls.Add(1) == 1 {
		close(f.entered)
	}
	select {
	case <-f.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type connectionFake struct {
	snapshot realtimev1.ConnectionSnapshot
	err      error
}

func (f *connectionFake) GetCurrent(ctx context.Context, _ string) (realtimev1.ConnectionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return realtimev1.ConnectionSnapshot{}, err
	}
	return f.snapshot, f.err
}

func (f *signalingFake) Offer(_ context.Context, token, sessionID string, request webrtc.OfferRequest) (webrtc.OfferResponse, error) {
	f.offerCalls++
	if token == "" || sessionID == "" || request.IdempotencyKey == "" {
		return webrtc.OfferResponse{}, webrtc.ErrRealtimeTokenRequired
	}
	return webrtc.OfferResponse{SDP: "answer-sdp", Type: "answer", SessionID: sessionID, ConnectionID: "connection-1", ConnectionState: realtimev1.ConnectionConnecting}, nil
}

func (f *signalingFake) AddCandidates(_ context.Context, token, sessionID string, request webrtc.CandidateRequest) (webrtc.CandidateResponse, error) {
	f.candidateCalls++
	if token == "" || sessionID == "" {
		return webrtc.CandidateResponse{}, webrtc.ErrRealtimeTokenRequired
	}
	if request.ConnectionID == "" {
		return webrtc.CandidateResponse{}, webrtc.ErrConnectionIDRequired
	}
	return webrtc.CandidateResponse{ConnectionID: request.ConnectionID, AcceptedCandidateIDs: []string{"candidate-1"}, EndOfCandidates: request.EndOfCandidates}, nil
}

type ticketFake struct {
	ticket webrtc.ConnectionTicket
	err    error
}

func (f *ticketFake) Validate(context.Context, string, string) (webrtc.ConnectionTicket, error) {
	return f.ticket, f.err
}

type sessionTicketFake struct {
	expiresAt time.Time
}

func (f *sessionTicketFake) Validate(_ context.Context, _ string, sessionID string) (webrtc.ConnectionTicket, error) {
	return webrtc.ConnectionTicket{SessionID: sessionID, AccountID: "account-1", ExpiresAt: f.expiresAt}, nil
}

type configFake struct {
	value WebRTCConfig
	err   error
}

func (f *configFake) GetConfig(context.Context, string) (WebRTCConfig, error) {
	return f.value, f.err
}
