//go:build integration

package recordstore

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHistoryQueryPlanUsesOrderedHistoryIndex(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	seedHistoryPlanFixtures(t, pool, 5000)

	var planJSON []byte
	err := pool.QueryRow(t.Context(), `
EXPLAIN (FORMAT JSON)
SELECT id
FROM voice_turns
WHERE session_id = ANY($1::text[])
  AND session_id = $2
ORDER BY created_at DESC, id DESC
LIMIT 21`, []string{"history_plan_session"}, "history_plan_session").Scan(&planJSON)
	if err != nil {
		t.Fatalf("EXPLAIN history query: %v", err)
	}
	var plan []struct {
		Plan map[string]any `json:"Plan"`
	}
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		t.Fatalf("decode history query plan: %v", err)
	}
	if len(plan) != 1 {
		t.Fatalf("plan entries = %d, want 1", len(plan))
	}
	if !planContainsAnyIndex(plan[0].Plan,
		"voice_turns_session_history_order_idx",
		"voice_turns_history_created_order_idx",
	) {
		t.Fatalf("history query plan does not use an ordered history index: %s", planJSON)
	}
}

func seedHistoryPlanFixtures(t *testing.T, pool *pgxpool.Pool, count int) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
INSERT INTO voice_turns (
    id, event_id, event_payload_hash, session_id, speaker_code, sequence_no,
    source_language, target_language, language_config_version, source_text,
    translated_text, attribution_status, started_at, ended_at, created_at
)
SELECT 'history_plan_turn_' || n,
       'history_plan_event_' || n,
       repeat(E'\\000', 32)::bytea,
       'history_plan_session',
       'speaker_pending', n,
       'zh-CN', 'en-US', 1, 'source', 'translation', 'pending',
       TIMESTAMPTZ '2026-07-28 09:00:00+00' + n * INTERVAL '1 second',
       TIMESTAMPTZ '2026-07-28 09:00:00+00' + n * INTERVAL '1 second',
       TIMESTAMPTZ '2026-07-28 09:00:00+00' + n * INTERVAL '1 second'
FROM generate_series(1, $1) AS series(n)`, count)
	if err != nil {
		t.Fatalf("seed history plan fixtures: %v", err)
	}
	_, err = pool.Exec(t.Context(), "ANALYZE voice_turns")
	if err != nil {
		t.Fatalf("analyze history plan fixtures: %v", err)
	}
}

func planContainsAnyIndex(node map[string]any, indexNames ...string) bool {
	for _, indexName := range indexNames {
		if node["Index Name"] == indexName {
			return true
		}
	}
	children, ok := node["Plans"].([]any)
	if !ok {
		return false
	}
	for _, child := range children {
		childMap, ok := child.(map[string]any)
		if ok && planContainsAnyIndex(childMap, indexNames...) {
			return true
		}
	}
	return false
}
