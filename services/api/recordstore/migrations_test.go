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
	if len(migrations) != 11 {
		t.Fatalf("len(embeddedMigrations()) = %d, want 11", len(migrations))
	}
	voiceRecords := migrations[0]
	if voiceRecords.Version != 1 || voiceRecords.Name != "voice_records" {
		t.Fatalf("migration = %#v, want version 1 named voice_records", voiceRecords)
	}
	for _, table := range []string{"voice_session_participants", "voice_turns"} {
		if !strings.Contains(voiceRecords.SQL, "CREATE TABLE "+table) {
			t.Fatalf("migration SQL does not create %s", table)
		}
	}
	for _, constraint := range []string{"event_payload_hash BYTEA NOT NULL", "octet_length(event_payload_hash) = 32"} {
		if !strings.Contains(voiceRecords.SQL, constraint) {
			t.Fatalf("migration SQL does not contain %q", constraint)
		}
	}

	controlPlane := migrations[1]
	if controlPlane.Version != 2 || controlPlane.Name != "member5_control_plane" {
		t.Fatalf("migration = %#v, want version 2 named member5_control_plane", controlPlane)
	}
	for _, table := range []string{
		"lingow_accounts", "lingow_phone_challenges", "lingow_auth_sessions", "voice_sessions",
		"voice_session_start_operations", "lingow_usage_records", "outbound_messages", "delivery_attempts", "delivery_outbox",
		"message_preferences", "account_destinations",
	} {
		if !strings.Contains(controlPlane.SQL, "CREATE TABLE "+table) {
			t.Fatalf("migration SQL does not create %s", table)
		}
	}
	for _, constraint := range []string{
		"CREATE UNIQUE INDEX lingow_accounts_phone_hash_key",
		"ON lingow_accounts (phone_hash)",
		"WHERE phone_hash IS NOT NULL",
		"cost_amount NUMERIC(20, 8)",
		"currency TEXT",
		"cost_amount IS NULL OR cost_amount >= 0",
		"currency IS NULL OR currency ~ '^[A-Z]{3}$'",
		"operation_id TEXT PRIMARY KEY",
		"status IN ('pending', 'compensating', 'completed', 'compensated', 'compensation_failed')",
		"CREATE UNIQUE INDEX voice_session_start_operations_one_unfinished_per_session",
		"WHERE status IN ('pending', 'compensating', 'compensation_failed')",
	} {
		if !strings.Contains(controlPlane.SQL, constraint) {
			t.Fatalf("control-plane migration SQL does not contain %q", constraint)
		}
	}
	if strings.Contains(controlPlane.SQL, "delivery_outbox_idempotency_key UNIQUE") {
		t.Fatal("delivery outbox must not make account-scoped idempotency keys globally unique")
	}
	if !strings.Contains(controlPlane.SQL, "CONSTRAINT delivery_outbox_attempt_key UNIQUE (attempt_id)") {
		t.Fatal("delivery outbox must keep attempt_id as the durable unique identity")
	}

	byVersion := make(map[int64]migration, len(migrations))
	for _, item := range migrations {
		byVersion[item.Version] = item
	}
	for version, content := range map[int64][]string{
		3: {"max_attempts", "lingow_phone_challenges_phone_created_idx"},
		4: {"lingow_account_lineage", "WITH RECURSIVE lineage"},
		5: {"phone_hash_v2", "lingow_accounts_phone_hash_v2_key", "expires_at = created_at + INTERVAL '1 second'"},
		6: {"SET phone_hash = NULL", "phone_hash_v2 IS NOT NULL"},
		7: {"SET cost_amount = NULL", "lingow_usage_records_pricing_pair_valid"},
		8: {"CREATE TABLE delivery_retry_requests", "delivery_retry_requests_account_key PRIMARY KEY", "delivery_retry_requests_attempt_key UNIQUE (attempt_id)"},
	} {
		item, ok := byVersion[version]
		if !ok {
			t.Fatalf("missing account-hardening migration version %d", version)
		}
		for _, expected := range content {
			if !strings.Contains(item.SQL, expected) {
				t.Fatalf("migration %d does not contain %q", version, expected)
			}
		}
	}

	finalTurnOutbox := migrations[8]
	if finalTurnOutbox.Version != 9 || finalTurnOutbox.Name != "final_turn_outbox" {
		t.Fatalf("migration = %#v, want version 9 named final_turn_outbox", finalTurnOutbox)
	}
	for _, constraint := range []string{
		"CREATE TABLE final_turn_outbox",
		"payload_hash BYTEA NOT NULL",
		"CONSTRAINT final_turn_outbox_status_valid",
		"CONSTRAINT final_turn_outbox_receipt_state_valid",
		"CREATE INDEX final_turn_outbox_available_idx",
		"CREATE TRIGGER final_turn_outbox_reject_payload_updates",
	} {
		if !strings.Contains(finalTurnOutbox.SQL, constraint) {
			t.Fatalf("final-turn outbox migration does not contain %q", constraint)
		}
	}

	sessionCompatibility := migrations[9]
	if sessionCompatibility.Version != 10 ||
		sessionCompatibility.Name != "session_start_operation_compatibility" {
		t.Fatalf(
			"migration = %#v, want version 10 named session_start_operation_compatibility",
			sessionCompatibility,
		)
	}
	for _, expected := range []string{
		"DEPRECATED: legacy Start request table",
		"CREATE TABLE voice_session_start_operations",
		"voice_session_start_operations_one_unfinished_per_session",
		"DROP CONSTRAINT IF EXISTS voice_sessions_timestamps_valid",
		"started_at IS NULL AND ended_at >= created_at",
		"missing one or more critical columns",
	} {
		if !strings.Contains(sessionCompatibility.SQL, expected) {
			t.Fatalf("session compatibility migration does not contain %q", expected)
		}
	}

	emailBind := migrations[10]
	if emailBind.Version != 11 || emailBind.Name != "email_bind_challenges" {
		t.Fatalf("migration = %#v, want version 11 named email_bind_challenges", emailBind)
	}
	if !strings.Contains(emailBind.SQL, "CREATE TABLE email_bind_challenges") {
		t.Fatal("email bind migration does not create email_bind_challenges")
	}
}
