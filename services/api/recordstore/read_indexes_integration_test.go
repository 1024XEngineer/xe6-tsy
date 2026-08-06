//go:build integration

package recordstore

import (
	"strings"
	"testing"
)

func TestRecordReadIndexesMatchKeysetOrder(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	want := map[string][]string{
		"voice_session_participants_session_speaker_order_idx": {"session_id", "speaker_code", "id"},
		"voice_turns_session_sequence_order_idx":               {"session_id", "sequence_no", "id"},
		"voice_turns_history_created_order_idx":                {"created_at DESC", "id DESC"},
		"voice_turns_session_history_order_idx":                {"session_id", "created_at DESC", "id DESC"},
	}
	rows, err := pool.Query(t.Context(), `
		SELECT indexname, indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname = ANY($1::text[])`, mapKeys(want))
	if err != nil {
		t.Fatalf("query read indexes: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatalf("scan read index: %v", err)
		}
		columns, ok := want[name]
		if !ok {
			t.Fatalf("unexpected read index %q", name)
		}
		position := 0
		for _, column := range columns {
			next := strings.Index(definition[position:], column)
			if next < 0 {
				t.Fatalf("index %s definition %q does not contain ordered column %q", name, definition, column)
			}
			position += next + len(column)
		}
		delete(want, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read index rows: %v", err)
	}
	if len(want) != 0 {
		t.Fatalf("missing read indexes: %#v", want)
	}
}

func mapKeys(values map[string][]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
