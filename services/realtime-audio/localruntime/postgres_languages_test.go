package localruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/jackc/pgx/v5"
)

type languageConfigQueryerStub struct {
	row       pgx.Row
	sessionID string
}

func (s *languageConfigQueryerStub) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if len(args) == 1 {
		s.sessionID, _ = args[0].(string)
	}
	return s.row
}

type languageConfigRowStub struct {
	version int64
	status  string
	raw     []byte
	err     error
}

func (r languageConfigRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*int64) = r.version
	*dest[1].(*string) = r.status
	*dest[2].(*[]byte) = append([]byte(nil), r.raw...)
	return nil
}

func TestDecodeLanguageConfigIncludesOutputRoutes(t *testing.T) {
	pairs, routes, err := decodeLanguageConfig([]byte(`{
        "language_pairs": [{"source":"zh-CN","target":"en-US"}],
        "output_routes": [{"target_language":"en-US","tts_enabled":false,"delivery_enabled":true}]
    }`))
	if err != nil {
		t.Fatalf("decodeLanguageConfig() error = %v", err)
	}
	if len(pairs) != 1 || pairs[0].Source != "zh-CN" || pairs[0].Target != "en-US" {
		t.Fatalf("pairs = %#v", pairs)
	}
	if len(routes) != 1 || routes[0].TargetLanguage != "en-US" || routes[0].TTSEnabled || !routes[0].DeliveryEnabled {
		t.Fatalf("routes = %#v", routes)
	}
}

func TestDecodeLanguageConfigRetainsLegacyPairArray(t *testing.T) {
	pairs, routes, err := decodeLanguageConfig([]byte(`[{"source":"zh-CN","target":"en-US"}]`))
	if err != nil {
		t.Fatalf("decodeLanguageConfig() error = %v", err)
	}
	if len(pairs) != 1 || len(routes) != 0 {
		t.Fatalf("pairs=%#v routes=%#v", pairs, routes)
	}
}

func TestPostgresLanguageConfigReaderReadsActiveConfig(t *testing.T) {
	now := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	pool := &languageConfigQueryerStub{row: languageConfigRowStub{
		version: 7,
		status:  "active",
		raw: []byte(`{
			"language_pairs": [
				{"source":" zh-CN ","target":" en-US "},
				{"source":"en-US","target":"en-US"},
				{"source":"","target":"ja-JP"}
			],
			"output_routes": [
				{"target_language":" en-US ","tts_enabled":true,"delivery_enabled":false},
				{"target_language":"","tts_enabled":true,"delivery_enabled":true}
			]
		}`),
	}}

	snapshot, err := (PostgresLanguageConfigReader{Pool: pool, Now: func() time.Time { return now }}).GetCurrentConfig(t.Context(), " session-1 ")
	if err != nil {
		t.Fatalf("GetCurrentConfig() error = %v", err)
	}
	if pool.sessionID != "session-1" {
		t.Fatalf("queried session ID = %q, want session-1", pool.sessionID)
	}
	wantPairs := []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}}
	if len(snapshot.LanguagePairs) != len(wantPairs) || snapshot.LanguagePairs[0] != wantPairs[0] {
		t.Fatalf("LanguagePairs = %#v, want %#v", snapshot.LanguagePairs, wantPairs)
	}
	if len(snapshot.OutputRoutes) != 1 || snapshot.OutputRoutes[0].TargetLanguage != "en-US" || !snapshot.OutputRoutes[0].TTSEnabled || snapshot.OutputRoutes[0].DeliveryEnabled {
		t.Fatalf("OutputRoutes = %#v", snapshot.OutputRoutes)
	}
	if snapshot.SessionID != "session-1" || snapshot.Version != 7 || snapshot.Status != "active" || !snapshot.UpdatedAt.Equal(now) {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPostgresLanguageConfigReaderRejectsUnavailableConfigs(t *testing.T) {
	tests := []struct {
		name string
		pool languageConfigQueryer
	}{
		{name: "query error", pool: &languageConfigQueryerStub{row: languageConfigRowStub{err: errors.New("database unavailable")}}},
		{name: "no active row", pool: &languageConfigQueryerStub{row: languageConfigRowStub{err: pgx.ErrNoRows}}},
		{name: "empty payload", pool: &languageConfigQueryerStub{row: languageConfigRowStub{version: 1, status: "active"}}},
		{name: "no usable pairs", pool: &languageConfigQueryerStub{row: languageConfigRowStub{version: 1, status: "active", raw: []byte(`[{"source":"en-US","target":"en-US"}]`)}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (PostgresLanguageConfigReader{Pool: tt.pool}).GetCurrentConfig(t.Context(), "session-1")
			if err == nil {
				t.Fatal("GetCurrentConfig() error = nil")
			}
		})
	}
}
