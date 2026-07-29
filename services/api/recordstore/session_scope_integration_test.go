//go:build integration

package recordstore

import (
	"slices"
	"testing"
)

func TestPostgresSessionScopeReaderFiltersByAccount(t *testing.T) {
	pool := testDatabase(t)
	if err := Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO lingow_accounts (id, kind) VALUES
			('account_scope_01', 'anonymous'),
			('account_scope_02', 'anonymous');
		INSERT INTO voice_sessions (id, account_id, status, audio_config, capabilities) VALUES
			('session_scope_01', 'account_scope_01', 'created', '{}'::jsonb, '{}'::jsonb),
			('session_scope_02', 'account_scope_02', 'created', '{}'::jsonb, '{}'::jsonb),
			('session_scope_03', 'account_scope_01', 'created', '{}'::jsonb, '{}'::jsonb)`); err != nil {
		t.Fatalf("insert session scope fixtures: %v", err)
	}

	reader, err := NewPostgresSessionScopeReader(pool)
	if err != nil {
		t.Fatalf("NewPostgresSessionScopeReader() error = %v", err)
	}
	ids, err := reader.SessionIDsForAccount(t.Context(), "account_scope_01")
	if err != nil {
		t.Fatalf("SessionIDsForAccount() error = %v", err)
	}
	slices.Sort(ids)
	want := []string{"session_scope_01", "session_scope_03"}
	if !slices.Equal(ids, want) {
		t.Fatalf("SessionIDsForAccount() = %#v, want %#v", ids, want)
	}
}
