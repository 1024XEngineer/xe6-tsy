package speech

import (
	"context"
	"errors"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

func TestBindingCoordinatorPreparesAndLeasesExactVersion(t *testing.T) {
	asrAdapter := asr.NewFakeProvider(asr.FakeProviderConfig{})
	ttsAdapter := tts.NewFakeProvider(tts.FakeProviderConfig{})
	coordinator := mustCoordinator(t, asrAdapter, ttsAdapter, staticResolver(t))

	if err := coordinator.Prepare(context.Background(), "session-1", 7, "zh-CN", "en-US"); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	binding, release, err := coordinator.AcquireForTurn(context.Background(), "session-1", 7)
	if err != nil {
		t.Fatalf("AcquireForTurn() error = %v", err)
	}
	if binding.SessionID != "session-1" || binding.LanguageConfigVersion != 7 {
		t.Fatalf("binding identity = %+v", binding)
	}
	if binding.ASR != asrAdapter || binding.TTS != ttsAdapter {
		t.Fatal("binding did not retain the registered adapter references")
	}
	if binding.ASRProfile.ID != "asr-primary" || binding.TTSProfile.ID != "tts-primary" {
		t.Fatalf("binding profiles = ASR %+v TTS %+v", binding.ASRProfile, binding.TTSProfile)
	}
	release()
	release()

	_, _, err = coordinator.AcquireForTurn(context.Background(), "session-1", 6)
	if !errors.Is(err, ErrBindingVersionConflict) {
		t.Fatalf("AcquireForTurn(old version) error = %v, want %v", err, ErrBindingVersionConflict)
	}
}

func TestBindingCoordinatorLateVersionCannotOverwriteNewerBinding(t *testing.T) {
	asrAdapter := asr.NewFakeProvider(asr.FakeProviderConfig{})
	ttsAdapter := tts.NewFakeProvider(tts.FakeProviderConfig{})
	resolver := newBlockingResolver()
	coordinator := mustCoordinator(t, asrAdapter, ttsAdapter, resolver)

	olderDone := make(chan error, 1)
	go func() {
		olderDone <- coordinator.Prepare(context.Background(), "session-1", 1, "zh-CN", "en-US")
	}()
	first := resolver.waitCall(t)

	newerDone := make(chan error, 1)
	go func() {
		newerDone <- coordinator.Prepare(context.Background(), "session-1", 2, "zh-CN", "en-US")
	}()
	second := resolver.waitCall(t)
	second.respond(staticRoute())
	if err := <-newerDone; err != nil {
		t.Fatalf("Prepare(newer) error = %v", err)
	}
	first.respond(staticRoute())
	if err := <-olderDone; !errors.Is(err, ErrBindingSuperseded) {
		t.Fatalf("Prepare(older) error = %v, want %v", err, ErrBindingSuperseded)
	}

	_, _, err := coordinator.AcquireForTurn(context.Background(), "session-1", 1)
	if !errors.Is(err, ErrBindingVersionConflict) {
		t.Fatalf("AcquireForTurn(version 1) error = %v, want %v", err, ErrBindingVersionConflict)
	}
	binding, release, err := coordinator.AcquireForTurn(context.Background(), "session-1", 2)
	if err != nil {
		t.Fatalf("AcquireForTurn(version 2) error = %v", err)
	}
	defer release()
	if binding.LanguageConfigVersion != 2 {
		t.Fatalf("binding version = %d, want 2", binding.LanguageConfigVersion)
	}
}

func TestBindingCoordinatorSharesPendingPreparationAndReturnsResolverFailure(t *testing.T) {
	asrAdapter := asr.NewFakeProvider(asr.FakeProviderConfig{})
	ttsAdapter := tts.NewFakeProvider(tts.FakeProviderConfig{})
	resolver := newBlockingResolver()
	coordinator := mustCoordinator(t, asrAdapter, ttsAdapter, resolver)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- coordinator.Prepare(context.Background(), "session-1", 1, "zh-CN", "en-US")
	}()
	call := resolver.waitCall(t)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- coordinator.Prepare(context.Background(), "session-1", 1, "zh-CN", "en-US")
	}()

	_, _, err := coordinator.AcquireForTurn(context.Background(), "session-1", 1)
	if !errors.Is(err, ErrBindingPending) {
		t.Fatalf("AcquireForTurn(pending) error = %v, want %v", err, ErrBindingPending)
	}
	call.respondErr(errors.New("resolver unavailable"))
	if err := <-firstDone; err == nil || err.Error() != "resolver unavailable" {
		t.Fatalf("first Prepare() error = %v, want resolver failure", err)
	}
	if err := <-secondDone; err == nil || err.Error() != "resolver unavailable" {
		t.Fatalf("second Prepare() error = %v, want shared resolver failure", err)
	}
	_, _, err = coordinator.AcquireForTurn(context.Background(), "session-1", 1)
	if !errors.Is(err, ErrBindingNotPrepared) {
		t.Fatalf("AcquireForTurn(failed prepare) error = %v, want %v", err, ErrBindingNotPrepared)
	}
}

func TestBindingCoordinatorRejectsSameVersionWithDifferentLanguagePair(t *testing.T) {
	coordinator := mustCoordinator(
		t,
		asr.NewFakeProvider(asr.FakeProviderConfig{}),
		tts.NewFakeProvider(tts.FakeProviderConfig{}),
		staticResolver(t),
	)
	if err := coordinator.Prepare(context.Background(), "session-1", 1, "zh-CN", "en-US"); err != nil {
		t.Fatalf("first Prepare() error = %v", err)
	}
	err := coordinator.Prepare(context.Background(), "session-1", 1, "ja-JP", "en-US")
	if !errors.Is(err, ErrBindingPreparationConflict) {
		t.Fatalf("Prepare(conflicting pair) error = %v, want %v", err, ErrBindingPreparationConflict)
	}
}

func TestBindingCoordinatorCloseSessionRemovesStateButKeepsExistingLease(t *testing.T) {
	coordinator := mustCoordinator(
		t,
		asr.NewFakeProvider(asr.FakeProviderConfig{}),
		tts.NewFakeProvider(tts.FakeProviderConfig{}),
		staticResolver(t),
	)
	if err := coordinator.Prepare(context.Background(), "session-1", 1, "zh-CN", "en-US"); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	_, release, err := coordinator.AcquireForTurn(context.Background(), "session-1", 1)
	if err != nil {
		t.Fatalf("AcquireForTurn() error = %v", err)
	}
	coordinator.CloseSession("session-1")
	_, _, err = coordinator.AcquireForTurn(context.Background(), "session-1", 1)
	if !errors.Is(err, ErrBindingNotPrepared) {
		t.Fatalf("AcquireForTurn(after close) error = %v, want %v", err, ErrBindingNotPrepared)
	}
	if err := coordinator.Prepare(context.Background(), "session-1", 2, "zh-CN", "en-US"); err != nil {
		t.Fatalf("Prepare(fresh session after close) error = %v", err)
	}
	binding, freshRelease, err := coordinator.AcquireForTurn(context.Background(), "session-1", 2)
	if err != nil {
		t.Fatalf("AcquireForTurn(fresh session after close) error = %v", err)
	}
	if binding.LanguageConfigVersion != 2 {
		t.Fatalf("fresh binding version = %d, want 2", binding.LanguageConfigVersion)
	}
	freshRelease()
	release()
}

func TestBindingCoordinatorCloseSessionCancelsPendingPreparation(t *testing.T) {
	resolver := &cancelAwareResolver{started: make(chan struct{})}
	coordinator := mustCoordinator(
		t,
		asr.NewFakeProvider(asr.FakeProviderConfig{}),
		tts.NewFakeProvider(tts.FakeProviderConfig{}),
		resolver,
	)

	prepared := make(chan error, 1)
	go func() {
		prepared <- coordinator.Prepare(context.Background(), "session-1", 1, "zh-CN", "en-US")
	}()

	select {
	case <-resolver.started:
	case <-t.Context().Done():
		t.Fatal("timed out waiting for resolver entry")
	}
	coordinator.CloseSession("session-1")
	if err := <-prepared; !errors.Is(err, ErrBindingSessionClosed) {
		t.Fatalf("Prepare() error = %v, want %v", err, ErrBindingSessionClosed)
	}
	_, _, err := coordinator.AcquireForTurn(context.Background(), "session-1", 1)
	if !errors.Is(err, ErrBindingNotPrepared) {
		t.Fatalf("AcquireForTurn() error = %v, want %v", err, ErrBindingNotPrepared)
	}
}

func TestBindingCoordinatorCloseSessionPreventsLatePreparationFromReplacingFreshSession(t *testing.T) {
	asrAdapter := asr.NewFakeProvider(asr.FakeProviderConfig{})
	ttsAdapter := tts.NewFakeProvider(tts.FakeProviderConfig{})
	resolver := newBlockingResolver()
	coordinator := mustCoordinator(t, asrAdapter, ttsAdapter, resolver)

	staleDone := make(chan error, 1)
	go func() {
		staleDone <- coordinator.Prepare(context.Background(), "session-1", 1, "zh-CN", "en-US")
	}()
	staleCall := resolver.waitCall(t)

	coordinator.CloseSession("session-1")

	freshDone := make(chan error, 1)
	go func() {
		freshDone <- coordinator.Prepare(context.Background(), "session-1", 2, "zh-CN", "en-US")
	}()
	freshCall := resolver.waitCall(t)
	freshCall.respond(staticRoute())
	if err := <-freshDone; err != nil {
		t.Fatalf("Prepare(fresh session) error = %v", err)
	}

	staleCall.respond(staticRoute())
	if err := <-staleDone; !errors.Is(err, ErrBindingSessionClosed) {
		t.Fatalf("Prepare(stale session) error = %v, want %v", err, ErrBindingSessionClosed)
	}

	binding, release, err := coordinator.AcquireForTurn(context.Background(), "session-1", 2)
	if err != nil {
		t.Fatalf("AcquireForTurn() error = %v", err)
	}
	defer release()
	if binding.LanguageConfigVersion != 2 {
		t.Fatalf("binding version = %d, want 2", binding.LanguageConfigVersion)
	}
}

func mustCoordinator(t *testing.T, asrAdapter asr.Provider, ttsAdapter tts.Provider, resolver RouteResolver) *BindingCoordinator {
	t.Helper()
	coordinator, err := NewBindingCoordinator(mustRegistry(t, asrAdapter, ttsAdapter, []string{"streaming"}), resolver)
	if err != nil {
		t.Fatalf("NewBindingCoordinator() error = %v", err)
	}
	return coordinator
}

func staticResolver(t *testing.T) RouteResolver {
	t.Helper()
	resolver, err := NewRouteResolver([]SpeechRoute{staticRoute()})
	if err != nil {
		t.Fatalf("NewRouteResolver() error = %v", err)
	}
	return resolver
}

func staticRoute() SpeechRoute {
	return SpeechRoute{
		LanguageA: "zh-CN", LanguageB: "en-US", ASRProfileID: "asr-primary", TTSProfileID: "tts-primary",
	}
}

type blockingResolver struct {
	calls chan blockingResolveCall
}

type cancelAwareResolver struct {
	started chan struct{}
}

func (r *cancelAwareResolver) ResolveBinding(ctx context.Context, _, _ string) (SpeechRoute, error) {
	close(r.started)
	<-ctx.Done()
	return SpeechRoute{}, ctx.Err()
}

type blockingResolveCall struct {
	result chan resolveResult
}

type resolveResult struct {
	route SpeechRoute
	err   error
}

func newBlockingResolver() *blockingResolver {
	return &blockingResolver{calls: make(chan blockingResolveCall, 2)}
}

func (r *blockingResolver) ResolveBinding(ctx context.Context, languageA, languageB string) (SpeechRoute, error) {
	call := blockingResolveCall{result: make(chan resolveResult, 1)}
	select {
	case r.calls <- call:
	case <-ctx.Done():
		return SpeechRoute{}, ctx.Err()
	}
	// Once a provider request has started it can still complete after context
	// cancellation. This fake models that late result so the version fence is
	// exercised without relying on wall-clock timing.
	result := <-call.result
	return result.route, result.err
}

func (r *blockingResolver) waitCall(t *testing.T) blockingResolveCall {
	t.Helper()
	select {
	case call := <-r.calls:
		return call
	case <-t.Context().Done():
		t.Fatal("timed out waiting for resolver call")
		return blockingResolveCall{}
	}
}

func (c blockingResolveCall) respond(route SpeechRoute) {
	c.result <- resolveResult{route: route}
}

func (c blockingResolveCall) respondErr(err error) {
	c.result <- resolveResult{err: err}
}
