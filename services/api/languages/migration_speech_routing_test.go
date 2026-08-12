package languages

import (
	"strings"
	"testing"
)

func TestSpeechRoutingMigrationSchemaContract(t *testing.T) {
	raw, err := migrationFS.ReadFile("migrations/005_speech_routing.sql")
	if err != nil {
		t.Fatalf("read speech routing migration: %v", err)
	}
	migration := string(raw)

	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS speech_language_pair_routes",
		"CHECK (language_a < language_b)",
		"FOREIGN KEY (language_a) REFERENCES supported_languages(language_code) ON DELETE RESTRICT",
		"FOREIGN KEY (language_b) REFERENCES supported_languages(language_code) ON DELETE RESTRICT",
		"FOREIGN KEY (asr_profile_id) REFERENCES speech_asr_profiles(id) ON DELETE RESTRICT",
		"FOREIGN KEY (tts_profile_id) REFERENCES speech_tts_profiles(id) ON DELETE RESTRICT",
		"FOREIGN KEY (profile_id) REFERENCES speech_asr_profiles(id) ON DELETE RESTRICT",
		"FOREIGN KEY (profile_id) REFERENCES speech_tts_profiles(id) ON DELETE RESTRICT",
		"FOREIGN KEY (language_code) REFERENCES supported_languages(language_code) ON DELETE RESTRICT",
		"CREATE UNIQUE INDEX IF NOT EXISTS speech_active_language_pair_route_unique",
		"ON speech_language_pair_routes (language_a, language_b)",
		"INSERT INTO speech_asr_profiles",
		"INSERT INTO speech_tts_profiles",
		"SELECT 'legacy-default'",
		"language_code = 'en-US'",
		"language_code = 'zh-CN'",
		"INSERT INTO speech_language_pair_routes",
	} {
		if !strings.Contains(migration, fragment) {
			t.Errorf("migration is missing %q", fragment)
		}
	}
	if strings.Contains(migration, "speech_routes") {
		t.Error("migration must use speech_language_pair_routes, not speech_routes")
	}
	if strings.Contains(migration, "priority") {
		t.Error("migration must not reintroduce priority-based routing")
	}

	assertSpeechProfileColumns(t, migration, "speech_asr_profiles", []string{
		"id",
		"provider_code",
		"model",
		"input_encoding",
		"input_sample_rate_hz",
		"input_channels",
		"enabled",
		"retired_at",
	})
	assertSpeechProfileColumns(t, migration, "speech_tts_profiles", []string{
		"id",
		"provider_code",
		"model",
		"voice_id",
		"output_encoding",
		"output_sample_rate_hz",
		"output_channels",
		"enabled",
		"retired_at",
	})
	assertSpeechRouteSchema(t, migration)
	assertActiveRouteIndex(t, migration)
}

func assertActiveRouteIndex(t *testing.T, migration string) {
	t.Helper()
	indexStart := "CREATE UNIQUE INDEX IF NOT EXISTS speech_active_language_pair_route_unique"
	start := strings.Index(migration, indexStart)
	if start < 0 {
		t.Fatal("migration has no active speech route index")
	}
	indexEnd := strings.Index(migration[start:], ";")
	if indexEnd < 0 {
		t.Fatal("migration has no end for active speech route index")
	}
	index := migration[start : start+indexEnd]
	for _, fragment := range []string{
		"ON speech_language_pair_routes (language_a, language_b)",
		"WHERE enabled = TRUE AND retired_at IS NULL",
	} {
		if !strings.Contains(index, fragment) {
			t.Errorf("active speech route index is missing %q", fragment)
		}
	}
}

func assertSpeechProfileColumns(t *testing.T, migration, table string, columns []string) {
	t.Helper()
	definitionStart := "CREATE TABLE IF NOT EXISTS " + table + " ("
	start := strings.Index(migration, definitionStart)
	if start < 0 {
		t.Fatalf("migration has no %s definition", table)
	}
	definitionEnd := strings.Index(migration[start:], "\n);")
	if definitionEnd < 0 {
		t.Fatalf("migration has no end for %s definition", table)
	}
	definition := migration[start : start+definitionEnd]
	for _, column := range columns {
		if !strings.Contains(definition, column) {
			t.Errorf("%s definition is missing %q", table, column)
		}
	}
	for _, removedColumn := range []string{"priority", "is_active"} {
		if strings.Contains(definition, removedColumn) {
			t.Errorf("%s definition unexpectedly contains %q", table, removedColumn)
		}
	}
}

func assertSpeechRouteSchema(t *testing.T, migration string) {
	t.Helper()
	definitionStart := "CREATE TABLE IF NOT EXISTS speech_language_pair_routes ("
	start := strings.Index(migration, definitionStart)
	if start < 0 {
		t.Fatal("migration has no speech_language_pair_routes definition")
	}
	definitionEnd := strings.Index(migration[start:], "\n);")
	if definitionEnd < 0 {
		t.Fatal("migration has no end for speech_language_pair_routes definition")
	}
	definition := migration[start : start+definitionEnd]
	for _, column := range []string{"id", "language_a", "language_b", "asr_profile_id", "tts_profile_id", "enabled", "retired_at"} {
		if !strings.Contains(definition, column) {
			t.Errorf("speech_language_pair_routes definition is missing %q", column)
		}
	}
	if !strings.Contains(definition, "id             VARCHAR(128) PRIMARY KEY") {
		t.Error("speech_language_pair_routes must use its durable id as the primary key")
	}
	if strings.Contains(definition, "PRIMARY KEY (language_a, language_b)") {
		t.Error("speech_language_pair_routes must not use the language pair as its primary key")
	}
	if !strings.Contains(definition, "CHECK (retired_at IS NULL OR enabled = FALSE)") {
		t.Error("speech_language_pair_routes must retire routes by disabling them")
	}
}
