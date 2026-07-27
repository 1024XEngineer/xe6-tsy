package recordstore

import (
	"strings"
	"testing"
)

func TestEmbeddedMigrations(t *testing.T) {
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatalf("embeddedMigrations() error = %v", err)
	}
	if len(migrations) != 1 {
		t.Fatalf("len(embeddedMigrations()) = %d, want 1", len(migrations))
	}
	migration := migrations[0]
	if migration.Version != 1 || migration.Name != "voice_records" {
		t.Fatalf("migration = %#v, want version 1 named voice_records", migration)
	}
	for _, table := range []string{"voice_session_participants", "voice_turns"} {
		if !strings.Contains(migration.SQL, "CREATE TABLE "+table) {
			t.Fatalf("migration SQL does not create %s", table)
		}
	}
}
