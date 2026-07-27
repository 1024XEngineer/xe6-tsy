package languages

import (
	"context"
	"errors"
	"testing"
)

func bilingualPairs() []LanguagePair {
	return []LanguagePair{
		{Source: "zh-CN", Target: "en-US"},
		{Source: "en-US", Target: "zh-CN"},
	}
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

	snap, err := svc.GetCurrentConfig(ctx, "vs_1")
	if err != nil {
		t.Fatalf("GetCurrentConfig: %v", err)
	}
	if snap.Version != 1 || len(snap.LanguagePairs) != 2 {
		t.Fatalf("unexpected snapshot: %#v", snap)
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

func TestValidateP0LanguagePairs(t *testing.T) {
	catalog := map[string]SupportedLanguage{
		"zh-CN": {LanguageCode: "zh-CN", SupportsAsSource: true, SupportsAsTarget: true},
		"en-US": {LanguageCode: "en-US", SupportsAsSource: true, SupportsAsTarget: true},
	}
	if err := validateP0LanguagePairs(bilingualPairs(), catalog); err != nil {
		t.Fatalf("valid pairs rejected: %v", err)
	}
}
