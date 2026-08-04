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
	if len(migrations) != 19 {
		t.Fatalf("len(embeddedMigrations()) = %d, want 19", len(migrations))
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

	wechatChannel := migrations[11]
	if wechatChannel.Version != 12 || wechatChannel.Name != "enable_wechat_channel" {
		t.Fatalf("migration = %#v, want version 12 named enable_wechat_channel", wechatChannel)
	}
	if !strings.Contains(wechatChannel.SQL, "channel IN ('email', 'wechat')") {
		t.Fatal("wechat channel migration does not allow wechat channel")
	}

	failedTerminalTimestamp := migrations[12]
	if failedTerminalTimestamp.Version != 13 ||
		failedTerminalTimestamp.Name != "session_failed_terminal_timestamp" {
		t.Fatalf(
			"migration = %#v, want version 13 named session_failed_terminal_timestamp",
			failedTerminalTimestamp,
		)
	}
	for _, expected := range []string{
		"SET ended_at = started_at",
		"status = 'failed'",
		"ended_at IS NOT NULL",
		"ended_at >= started_at",
	} {
		if !strings.Contains(failedTerminalTimestamp.SQL, expected) {
			t.Fatalf("failed-terminal migration does not contain %q", expected)
		}
	}

	endRecovery := migrations[13]
	if endRecovery.Version != 14 || endRecovery.Name != "end_intent_recovery" {
		t.Fatalf("migration = %#v, want version 14 named end_intent_recovery", endRecovery)
	}
	for _, expected := range []string{
		"ADD COLUMN trace_id",
		"ADD COLUMN retry_count",
		"ADD COLUMN next_attempt_at",
		"ADD COLUMN recovery_owner",
		"LEAST(requested_at, clock_timestamp())",
		"voice_session_end_intents_recovery_due_idx",
	} {
		if !strings.Contains(endRecovery.SQL, expected) {
			t.Fatalf("end-recovery migration does not contain %q", expected)
		}
	}

	attributionSnapshot := migrations[14]
	if attributionSnapshot.Version != 15 || attributionSnapshot.Name != "attribution_snapshot_updates" {
		t.Fatalf("migration = %#v, want version 15 named attribution_snapshot_updates", attributionSnapshot)
	}
	for _, expected := range []string{
		"CREATE OR REPLACE FUNCTION recordstore_reject_voice_turn_immutable_updates",
		"NEW.sequence_no IS DISTINCT FROM OLD.sequence_no",
		"NEW.source_text IS DISTINCT FROM OLD.source_text",
		"NEW.translated_text IS DISTINCT FROM OLD.translated_text",
	} {
		if !strings.Contains(attributionSnapshot.SQL, expected) {
			t.Fatalf("attribution-snapshot migration does not contain %q", expected)
		}
	}
	if strings.Contains(attributionSnapshot.SQL, "NEW.speaker_code IS DISTINCT FROM OLD.speaker_code") {
		t.Fatal("attribution-snapshot migration must allow speaker snapshot field updates")
	}

	attributionTasks := migrations[15]
	if attributionTasks.Version != 16 || attributionTasks.Name != "attribution_tasks" {
		t.Fatalf("migration = %#v, want version 16 named attribution_tasks", attributionTasks)
	}
	for _, expected := range []string{
		"CREATE TABLE attribution_tasks",
		"task_type IN ('participant_mapping', 'turn_attribution')",
		"status IN ('pending', 'processing', 'completed', 'failed')",
		"CONSTRAINT attribution_tasks_turn_id_key UNIQUE (turn_id)",
		"CREATE INDEX attribution_tasks_available_idx",
		"CREATE INDEX attribution_tasks_lease_idx",
	} {
		if !strings.Contains(attributionTasks.SQL, expected) {
			t.Fatalf("attribution-tasks migration does not contain %q", expected)
		}
	}

	backfill := migrations[16]
	if backfill.Version != 17 || backfill.Name != "backfill_attribution_tasks" {
		t.Fatalf("migration = %#v, want version 17 named backfill_attribution_tasks", backfill)
	}
	for _, expected := range []string{
		"INSERT INTO attribution_tasks",
		"no_provider_speaker_id",
		"ON CONFLICT (turn_id) DO NOTHING",
	} {
		if !strings.Contains(backfill.SQL, expected) {
			t.Fatalf("backfill migration does not contain %q", expected)
		}
	}

	deadLetter := migrations[17]
	if deadLetter.Version != 18 || deadLetter.Name != "final_turn_outbox_dead_letter" {
		t.Fatalf("migration = %#v, want version 18 named final_turn_outbox_dead_letter", deadLetter)
	}
	for _, expected := range []string{
		"ADD COLUMN last_error TEXT",
		"ADD COLUMN rejected_at TIMESTAMPTZ",
	} {
		if !strings.Contains(deadLetter.SQL, expected) {
			t.Fatalf("dead-letter migration does not contain %q", expected)
		}
	}

	historyIndexes := migrations[18]
	if historyIndexes.Version != 19 || historyIndexes.Name != "record_history_indexes" {
		t.Fatalf("migration = %#v, want version 19 named record_history_indexes", historyIndexes)
	}
	if !strings.Contains(historyIndexes.SQL, "voice_turns_session_history_order_idx") {
		t.Fatal("history index migration does not create voice_turns_session_history_order_idx")
	}

}
