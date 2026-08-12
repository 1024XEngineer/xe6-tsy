package speech

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/text/language"
)

var (
	// ErrLanguageRequired rejects incomplete language-pair routing requests.
	ErrLanguageRequired = errors.New("speech route language is required")
	// ErrLanguageInvalid rejects a language value that is not a BCP-47 tag.
	ErrLanguageInvalid = errors.New("speech route language is invalid")
	// ErrLanguagePairInvalid rejects routes with the same language on both sides.
	ErrLanguagePairInvalid = errors.New("speech route language pair is invalid")
	// ErrSpeechRouteNotFound indicates that no route supports the requested language pair.
	ErrSpeechRouteNotFound = errors.New("speech route not found")
	// ErrDuplicateSpeechRoute prevents one language pair from selecting multiple bindings.
	ErrDuplicateSpeechRoute = errors.New("duplicate speech route")
	// ErrSpeechRouteInvalid indicates that a resolver returned incomplete route data.
	ErrSpeechRouteInvalid = errors.New("speech route is invalid")
	// ErrSpeechRouteMismatch indicates that a resolver returned a route for another language pair.
	ErrSpeechRouteMismatch = errors.New("speech route does not match requested language pair")
)

// SpeechRoute maps one bidirectional language pair to its ASR and TTS profiles.
// LanguageA and LanguageB are unordered for resolution but retained as configured
// so logs and callers can retain the canonical configuration spelling.
type SpeechRoute struct {
	LanguageA    string
	LanguageB    string
	ASRProfileID string
	TTSProfileID string
}

// RouteResolver resolves the profile route for a language configuration snapshot.
type RouteResolver interface {
	ResolveBinding(ctx context.Context, languageA, languageB string) (SpeechRoute, error)
}

// StaticRouteResolver is the in-memory route implementation for explicit service wiring.
type StaticRouteResolver struct {
	routes map[string]SpeechRoute
}

// NewRouteResolver validates and copies a fixed set of bidirectional speech routes.
func NewRouteResolver(routes []SpeechRoute) (*StaticRouteResolver, error) {
	resolver := &StaticRouteResolver{routes: make(map[string]SpeechRoute, len(routes))}
	for _, configured := range routes {
		route, err := normalizeRoute(configured)
		if err != nil {
			return nil, err
		}
		key, err := routeKey(route.LanguageA, route.LanguageB)
		if err != nil {
			return nil, err
		}
		if _, exists := resolver.routes[key]; exists {
			return nil, ErrDuplicateSpeechRoute
		}
		resolver.routes[key] = route
	}
	return resolver, nil
}

// ResolveBinding returns the route for either ordering of one language pair.
func (r *StaticRouteResolver) ResolveBinding(ctx context.Context, languageA, languageB string) (SpeechRoute, error) {
	if err := ctx.Err(); err != nil {
		return SpeechRoute{}, err
	}
	if r == nil {
		return SpeechRoute{}, ErrRouteResolverRequired
	}
	key, err := routeKey(languageA, languageB)
	if err != nil {
		return SpeechRoute{}, err
	}
	route, ok := r.routes[key]
	if !ok {
		return SpeechRoute{}, fmt.Errorf("%w: %s and %s", ErrSpeechRouteNotFound, languageA, languageB)
	}
	return route, nil
}

func normalizeRoute(route SpeechRoute) (SpeechRoute, error) {
	var err error
	route.LanguageA, err = canonicalLanguage(route.LanguageA)
	if err != nil {
		return SpeechRoute{}, err
	}
	route.LanguageB, err = canonicalLanguage(route.LanguageB)
	if err != nil {
		return SpeechRoute{}, err
	}
	route.ASRProfileID = strings.TrimSpace(route.ASRProfileID)
	route.TTSProfileID = strings.TrimSpace(route.TTSProfileID)
	if _, err := routeKey(route.LanguageA, route.LanguageB); err != nil {
		return SpeechRoute{}, err
	}
	if route.LanguageA > route.LanguageB {
		route.LanguageA, route.LanguageB = route.LanguageB, route.LanguageA
	}
	if route.ASRProfileID == "" || route.TTSProfileID == "" {
		return SpeechRoute{}, ErrSpeechRouteInvalid
	}
	return route, nil
}

func routeKey(languageA, languageB string) (string, error) {
	var err error
	languageA, err = canonicalLanguage(languageA)
	if err != nil {
		return "", err
	}
	languageB, err = canonicalLanguage(languageB)
	if err != nil {
		return "", err
	}
	if languageA == languageB {
		return "", ErrLanguagePairInvalid
	}
	if languageA > languageB {
		languageA, languageB = languageB, languageA
	}
	return languageA + "\x00" + languageB, nil
}

func canonicalLanguage(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ErrLanguageRequired
	}
	tag, err := language.Parse(strings.ReplaceAll(value, "_", "-"))
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrLanguageInvalid, value)
	}
	return tag.String(), nil
}
