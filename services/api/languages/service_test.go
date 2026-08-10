package languages

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func bilingualPairs() []LanguagePair {
	return []LanguagePair{
		{Source: "zh-CN", Target: "en-US"},
		{Source: "en-US", Target: "zh-CN"},
	}
}

type deliveryReadinessStub struct {
	ready bool
	err   error
	calls int
}

func (s *deliveryReadinessStub) HasReadyAutomaticTarget(context.Context, string) (bool, error) {
	s.calls++
	return s.ready, s.err
}

func TestServiceCreateAndReadLifecycle(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_1": "acct_1"})
	ctx := context.Background()

	created, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "ik_1", CreateLanguageConfigRequest{
		Languages: bilingualPairs(),
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	if created.Version != 1 || created.Status != StatusActive {
		t.Fatalf("unexpected create result: %#v", created)
	}
	if created.OutputMode != InterpretationOutputModeBidirectional {
		t.Fatalf("created output mode = %q, want %q", created.OutputMode, InterpretationOutputModeBidirectional)
	}

	snap, err := svc.GetCurrentConfig(ctx, "vs_1")
	if err != nil {
		t.Fatalf("GetCurrentConfig: %v", err)
	}
	if snap.Version != 1 || len(snap.LanguagePairs) != 2 || len(snap.OutputRoutes) != 2 {
		t.Fatalf("unexpected snapshot: %#v", snap)
	}
	for _, route := range snap.OutputRoutes {
		if !route.TTSEnabled || route.DeliveryEnabled {
			t.Fatalf("legacy-compatible output route = %#v", route)
		}
	}

	target, version, err := svc.ResolveTarget(ctx, "vs_1", "zh-CN")
	if err != nil || target != "en-US" || version != 1 {
		t.Fatalf("ResolveTarget = %q %d %v", target, version, err)
	}

	expected := 1
	second, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "ik_2", CreateLanguageConfigRequest{
		Languages:       bilingualPairs(),
		ExpectedVersion: &expected,
	})
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("want version 2, got %d", second.Version)
	}
}

func TestServicePersistsPerTargetOutputRoutes(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_1": "acct_1"}, &deliveryReadinessStub{ready: true})
	created, err := svc.CreateConfig(t.Context(), "acct_1", "vs_1", "routes-1", CreateLanguageConfigRequest{
		Languages: bilingualPairs(),
		OutputRoutes: []OutputRoute{
			{TargetLanguage: "en-US", TTSEnabled: true, DeliveryEnabled: false},
			{TargetLanguage: "zh-CN", TTSEnabled: false, DeliveryEnabled: true},
		},
	})
	if err != nil {
		t.Fatalf("CreateConfig() error = %v", err)
	}
	if len(created.OutputRoutes) != 2 || created.OutputRoutes[0].DeliveryEnabled || !created.OutputRoutes[1].DeliveryEnabled {
		t.Fatalf("created output routes = %#v", created.OutputRoutes)
	}
	if created.OutputMode != InterpretationOutputModeSingle {
		t.Fatalf("created output mode = %q, want %q", created.OutputMode, InterpretationOutputModeSingle)
	}
	snapshot, err := svc.GetCurrentConfig(t.Context(), "vs_1")
	if err != nil {
		t.Fatalf("GetCurrentConfig() error = %v", err)
	}
	if len(snapshot.OutputRoutes) != 2 || snapshot.OutputRoutes[1].TTSEnabled {
		t.Fatalf("snapshot output routes = %#v", snapshot.OutputRoutes)
	}
}

func TestServiceValidatesInterpretationOutputPresets(t *testing.T) {
	tests := []struct {
		name    string
		routes  []OutputRoute
		wantErr bool
	}{
		{
			name: "bidirectional_tts",
			routes: []OutputRoute{
				{TargetLanguage: "en-US", TTSEnabled: true},
				{TargetLanguage: "zh-CN", TTSEnabled: true},
			},
		},
		{
			name: "single_zh_to_en",
			routes: []OutputRoute{
				{TargetLanguage: "en-US", TTSEnabled: true},
				{TargetLanguage: "zh-CN", DeliveryEnabled: true},
			},
		},
		{
			name: "single_en_to_zh",
			routes: []OutputRoute{
				{TargetLanguage: "en-US", DeliveryEnabled: true},
				{TargetLanguage: "zh-CN", TTSEnabled: true},
			},
		},
		{
			name: "tts_and_delivery_enabled",
			routes: []OutputRoute{
				{TargetLanguage: "en-US", TTSEnabled: true, DeliveryEnabled: true},
				{TargetLanguage: "zh-CN", TTSEnabled: true},
			},
			wantErr: true,
		},
		{
			name: "both_outputs_disabled",
			routes: []OutputRoute{
				{TargetLanguage: "en-US"},
				{TargetLanguage: "zh-CN", TTSEnabled: true},
			},
			wantErr: true,
		},
		{
			name: "delivery_enabled_for_both_targets",
			routes: []OutputRoute{
				{TargetLanguage: "en-US", DeliveryEnabled: true},
				{TargetLanguage: "zh-CN", DeliveryEnabled: true},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore(nil, nil)
			svc := NewService(store, MapSessionOwner{"vs_1": "acct_1"}, &deliveryReadinessStub{ready: true})
			_, err := svc.CreateConfig(t.Context(), "acct_1", "vs_1", "", CreateLanguageConfigRequest{
				Languages:    bilingualPairs(),
				OutputRoutes: tt.routes,
			})
			if tt.wantErr && !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("CreateConfig() error = %v, want invalid_request", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("CreateConfig() error = %v", err)
			}
		})
	}
}

func TestServiceRequiresReadyDeliveryTargetForSingleOutput(t *testing.T) {
	singleRoutes := []OutputRoute{
		{TargetLanguage: "en-US", TTSEnabled: true},
		{TargetLanguage: "zh-CN", DeliveryEnabled: true},
	}

	t.Run("ready target", func(t *testing.T) {
		readiness := &deliveryReadinessStub{ready: true}
		svc := NewService(NewMemoryStore(nil, nil), MapSessionOwner{"vs_1": "acct_1"}, readiness)
		_, err := svc.CreateConfig(t.Context(), "acct_1", "vs_1", "", CreateLanguageConfigRequest{
			Languages: bilingualPairs(), OutputRoutes: singleRoutes,
		})
		if err != nil {
			t.Fatalf("CreateConfig() error = %v", err)
		}
		if readiness.calls != 1 {
			t.Fatalf("readiness calls = %d, want 1", readiness.calls)
		}
	})

	t.Run("target unavailable", func(t *testing.T) {
		readiness := &deliveryReadinessStub{}
		svc := NewService(NewMemoryStore(nil, nil), MapSessionOwner{"vs_1": "acct_1"}, readiness)
		_, err := svc.CreateConfig(t.Context(), "acct_1", "vs_1", "", CreateLanguageConfigRequest{
			Languages: bilingualPairs(), OutputRoutes: singleRoutes,
		})
		if !errors.Is(err, ErrDeliveryTargetRequired) {
			t.Fatalf("CreateConfig() error = %v, want delivery_target_required", err)
		}
	})

	t.Run("bidirectional skips readiness", func(t *testing.T) {
		readiness := &deliveryReadinessStub{err: errors.New("must not be called")}
		svc := NewService(NewMemoryStore(nil, nil), MapSessionOwner{"vs_1": "acct_1"}, readiness)
		_, err := svc.CreateConfig(t.Context(), "acct_1", "vs_1", "", CreateLanguageConfigRequest{
			Languages: bilingualPairs(),
		})
		if err != nil {
			t.Fatalf("CreateConfig() error = %v", err)
		}
		if readiness.calls != 0 {
			t.Fatalf("readiness calls = %d, want 0", readiness.calls)
		}
	})

	t.Run("idempotent replay skips readiness", func(t *testing.T) {
		readiness := &deliveryReadinessStub{ready: true}
		svc := NewService(NewMemoryStore(nil, nil), MapSessionOwner{"vs_1": "acct_1"}, readiness)
		req := CreateLanguageConfigRequest{Languages: bilingualPairs(), OutputRoutes: singleRoutes}
		if _, err := svc.CreateConfig(t.Context(), "acct_1", "vs_1", "single-replay", req); err != nil {
			t.Fatalf("first CreateConfig() error = %v", err)
		}
		readiness.ready = false
		if _, err := svc.CreateConfig(t.Context(), "acct_1", "vs_1", "single-replay", req); err != nil {
			t.Fatalf("replay CreateConfig() error = %v", err)
		}
		if readiness.calls != 1 {
			t.Fatalf("readiness calls = %d, want 1", readiness.calls)
		}
	})
}

func TestServiceValidationAndAuthErrors(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_1": "acct_1"})
	ctx := context.Background()

	t.Run("forbidden", func(t *testing.T) {
		_, err := svc.CreateConfig(ctx, "acct_other", "vs_1", "", CreateLanguageConfigRequest{
			Languages: bilingualPairs(),
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("error = %v, want forbidden", err)
		}
	})

	t.Run("session_not_found", func(t *testing.T) {
		_, err := svc.CreateConfig(ctx, "acct_1", "vs_missing", "", CreateLanguageConfigRequest{
			Languages: bilingualPairs(),
		})
		if !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("error = %v, want session_not_found", err)
		}
	})

	t.Run("invalid_pair", func(t *testing.T) {
		_, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "", CreateLanguageConfigRequest{
			Languages: []LanguagePair{{Source: "zh-CN", Target: "zh-CN"}},
		})
		if !errors.Is(err, ErrInvalidLanguagePair) {
			t.Fatalf("error = %v, want invalid_language_pair", err)
		}
	})

	t.Run("unsupported_language", func(t *testing.T) {
		_, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "", CreateLanguageConfigRequest{
			Languages: []LanguagePair{
				{Source: "zh-CN", Target: "ja-JP"},
				{Source: "ja-JP", Target: "zh-CN"},
			},
		})
		if !errors.Is(err, ErrUnsupportedLanguage) {
			t.Fatalf("error = %v, want unsupported_language", err)
		}
	})

	t.Run("idempotency_key_too_long", func(t *testing.T) {
		longKey := make([]byte, MaxIdempotencyKeyLen+1)
		for i := range longKey {
			longKey[i] = 'a'
		}
		_, err := svc.CreateConfig(ctx, "acct_1", "vs_1", string(longKey), CreateLanguageConfigRequest{
			Languages: bilingualPairs(),
		})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("error = %v, want invalid_request", err)
		}
	})
}

func TestServiceIdempotency(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_1": "acct_1", "vs_2": "acct_1"})
	ctx := context.Background()

	first, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "ik_same", CreateLanguageConfigRequest{
		Languages: bilingualPairs(),
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	replay, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "ik_same", CreateLanguageConfigRequest{
		Languages: bilingualPairs(),
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.ID != first.ID || replay.Version != first.Version {
		t.Fatalf("replay should return original config")
	}

	_, err = svc.CreateConfig(ctx, "acct_1", "vs_2", "ik_same", CreateLanguageConfigRequest{
		Languages: bilingualPairs(),
	})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want idempotency_conflict", err)
	}

	t.Run("invalid_body_before_replay", func(t *testing.T) {
		_, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "ik_same", CreateLanguageConfigRequest{
			Languages: []LanguagePair{
				{Source: "zh-CN", Target: "en-US"},
				{Source: "zh-CN", Target: "en-US"},
			},
		})
		if !errors.Is(err, ErrInvalidLanguagePair) {
			t.Fatalf("error = %v, want invalid_language_pair", err)
		}
	})

	t.Run("expected_version_change_conflicts", func(t *testing.T) {
		expected := 1
		_, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "ik_same", CreateLanguageConfigRequest{
			Languages:       bilingualPairs(),
			ExpectedVersion: &expected,
		})
		if !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("error = %v, want idempotency_conflict", err)
		}
	})
}

func TestServiceVersionConflict(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_1": "acct_1"})
	ctx := context.Background()

	if _, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "", CreateLanguageConfigRequest{
		Languages: bilingualPairs(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	wrong := 9
	_, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "", CreateLanguageConfigRequest{
		Languages:       bilingualPairs(),
		ExpectedVersion: &wrong,
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("error = %v, want version_conflict", err)
	}
}

func TestServiceConcurrentIdenticalIdempotentRetry(t *testing.T) {
	base := NewMemoryStore(nil, nil)
	store := &concurrentIdempotencyStore{MemoryStore: base}
	svc := NewService(store, MapSessionOwner{"vs_1": "acct_1"})
	ctx := context.Background()
	req := CreateLanguageConfigRequest{Languages: bilingualPairs()}

	start := make(chan struct{})
	type result struct {
		cfg LanguageConfig
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			cfg, err := svc.CreateConfig(ctx, "acct_1", "vs_1", "ik_race", req)
			results <- result{cfg: cfg, err: err}
		}()
	}
	close(start)

	var success []LanguageConfig
	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("concurrent identical retry error = %v, want success", got.err)
		}
		success = append(success, got.cfg)
	}
	if success[0].ID != success[1].ID || success[0].Version != success[1].Version {
		t.Fatalf("loser should recover the winner config: %#v vs %#v", success[0], success[1])
	}
}

func TestValidateP0LanguagePairs(t *testing.T) {
	catalog := map[string]SupportedLanguage{
		"zh-CN": {LanguageCode: "zh-CN", SupportsAsSource: true, SupportsAsTarget: true},
		"en-US": {LanguageCode: "en-US", SupportsAsSource: true, SupportsAsTarget: true},
	}
	if err := validateP0LanguagePairs(bilingualPairs(), catalog); err != nil {
		t.Fatalf("valid pairs rejected: %v", err)
	}
}

func TestServiceListConfigHistory(t *testing.T) {
	store := &historyStoreFake{
		MemoryStore: NewMemoryStore(nil, nil),
		items: []LanguageConfig{
			{SessionID: "vs_1", Version: 2},
			{SessionID: "vs_1", Version: 1},
		},
		nextCursor: "1",
	}
	svc := NewService(store, MapSessionOwner{"vs_1": "acct_1"})

	items, next, err := svc.ListConfigHistory(t.Context(), "acct_1", "vs_1", "2", 1)
	if err != nil {
		t.Fatalf("ListConfigHistory() error = %v", err)
	}
	if !store.called {
		t.Fatal("ListConfigs() was not called")
	}
	if store.query.SessionID != "vs_1" || store.query.Cursor != "2" || store.query.Limit != 1 {
		t.Fatalf("ListConfigs query = %#v", store.query)
	}
	if len(items) != 2 || items[0].Version != 2 || next != "1" {
		t.Fatalf("items=%#v next=%q", items, next)
	}
}

func TestServiceListConfigHistoryRejectsUnauthorized(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		want      error
	}{
		{name: "missing_account", want: ErrUnauthenticated},
		{name: "forbidden", accountID: "acct_other", want: ErrForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &historyStoreFake{MemoryStore: NewMemoryStore(nil, nil)}
			svc := NewService(store, MapSessionOwner{"vs_1": "acct_1"})

			_, _, err := svc.ListConfigHistory(t.Context(), tt.accountID, "vs_1", "", 20)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ListConfigHistory() error = %v, want %v", err, tt.want)
			}
			if store.called {
				t.Fatal("ListConfigs() should not be called")
			}
		})
	}
}

// concurrentIdempotencyStore forces both callers to miss the pre-insert lookup,
// then lets both enter CreateActiveConfig so one hits ErrIdempotencyConflict.
type concurrentIdempotencyStore struct {
	*MemoryStore
	mu           sync.Mutex
	lookups      int
	creators     int
	lookupsDone  chan struct{}
	secondCreate chan struct{}
}

func (s *concurrentIdempotencyStore) GetConfigByIdempotencyKey(ctx context.Context, key string) (LanguageConfig, error) {
	s.mu.Lock()
	s.lookups++
	n := s.lookups
	if s.lookupsDone == nil {
		s.lookupsDone = make(chan struct{})
	}
	done := s.lookupsDone
	if n == 2 {
		close(done)
	}
	s.mu.Unlock()

	<-done
	if n <= 2 {
		return LanguageConfig{}, ErrNoActiveConfig
	}
	return s.MemoryStore.GetConfigByIdempotencyKey(ctx, key)
}

func (s *concurrentIdempotencyStore) CreateActiveConfig(ctx context.Context, input CreateConfigInput) (LanguageConfig, error) {
	s.mu.Lock()
	s.creators++
	n := s.creators
	if s.secondCreate == nil {
		s.secondCreate = make(chan struct{})
	}
	second := s.secondCreate
	s.mu.Unlock()

	if n == 1 {
		<-second
	} else if n == 2 {
		close(second)
	}
	return s.MemoryStore.CreateActiveConfig(ctx, input)
}

type historyStoreFake struct {
	*MemoryStore
	called     bool
	query      ListConfigsQuery
	items      []LanguageConfig
	nextCursor string
	err        error
}

func (s *historyStoreFake) ListConfigs(_ context.Context, query ListConfigsQuery) ([]LanguageConfig, string, error) {
	s.called = true
	s.query = query
	return append([]LanguageConfig(nil), s.items...), s.nextCursor, s.err
}
