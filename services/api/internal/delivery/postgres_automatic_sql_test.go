package delivery

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAutomaticPostgresReadQueries(t *testing.T) {
	run := testAutomaticRun()
	settlement := AutomaticTurnSettlement{
		AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1", TargetLanguage: "zh-CN",
		Channel: ChannelWeChat, DestinationRef: "primary-wechat", Status: AutomaticTurnSettlementFailed,
		MessageID: "message-1", CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
	pool := &automaticPostgresPoolFake{
		rows: []*automaticRowsFake{
			{rows: [][]any{automaticRunValues(run)}},
			{rows: [][]any{automaticRunValues(run)}},
			{rows: [][]any{automaticRunValues(run)}},
			{rows: [][]any{automaticSettlementValues(settlement)}},
		},
		row: []*automaticRowFake{{values: automaticRunValues(run)}},
	}
	repository := &PostgresRepository{pool: pool}
	if got, err := repository.GetAutomaticTurnRun(t.Context(), run.AccountID, run.TurnID); err != nil || got != run {
		t.Fatalf("GetAutomaticTurnRun() = %#v, %v; want %#v", got, err, run)
	}
	if got, err := repository.ListAutomaticTurnRetryCandidates(t.Context(), 5); err != nil || len(got) != 1 || got[0] != run {
		t.Fatalf("ListAutomaticTurnRetryCandidates() = %#v, %v", got, err)
	}
	if got, err := repository.ListAutomaticTurnRecoveryCandidates(t.Context(), 5); err != nil || len(got) != 1 || got[0] != run {
		t.Fatalf("ListAutomaticTurnRecoveryCandidates() = %#v, %v", got, err)
	}
	if got, err := repository.ListAutomaticTurnRestoreCandidates(t.Context(), 5); err != nil || len(got) != 1 || got[0] != run {
		t.Fatalf("ListAutomaticTurnRestoreCandidates() = %#v, %v", got, err)
	}
	if got, err := repository.ListAutomaticTurnSettlements(t.Context(), settlement.AccountID, settlement.TurnID); err != nil || !reflect.DeepEqual(got, []AutomaticTurnSettlement{settlement}) {
		t.Fatalf("ListAutomaticTurnSettlements() = %#v, %v", got, err)
	}
}

func TestAutomaticPostgresRetryCandidatesExcludeTotalFailures(t *testing.T) {
	pool := &automaticPostgresPoolFake{rows: []*automaticRowsFake{{}}}
	if _, err := (&PostgresRepository{pool: pool}).ListAutomaticTurnRetryCandidates(t.Context(), 5); err != nil {
		t.Fatalf("ListAutomaticTurnRetryCandidates() error = %v", err)
	}
	if len(pool.queries) != 1 {
		t.Fatalf("queries = %#v, want one query", pool.queries)
	}
	if !strings.Contains(pool.queries[0], "WHERE status='partially_succeeded'") {
		t.Fatalf("retry candidate query = %q, want partial-success filter", pool.queries[0])
	}
	if strings.Contains(pool.queries[0], "status IN ('partially_succeeded','failed')") {
		t.Fatalf("retry candidate query = %q, must exclude total failures", pool.queries[0])
	}
}

func TestAutomaticPostgresSchedulesTargetsAndClaimsFallback(t *testing.T) {
	run := testAutomaticRun()
	now := run.CreatedAt
	record := AutomaticTurnScheduleRecord{
		Run: run,
		Targets: []AutomaticTargetRecord{{
			Message:        Message{ID: "message-1", AccountID: run.AccountID, Channel: ChannelWeChat, DestinationRef: "primary-wechat", SnapshotVersion: 1, Turns: []FinalTurnSnapshot{{TurnID: run.TurnID}}, Status: MessageStatusQueued, Attempts: 1, CreatedAt: now, UpdatedAt: now},
			InitialAttempt: DeliveryAttempt{ID: "attempt-1", MessageID: "message-1", AttemptNumber: 1, Status: AttemptStatusQueued, CreatedAt: now},
			Settlement:     AutomaticTurnSettlement{AccountID: run.AccountID, TurnID: run.TurnID, SessionID: run.SessionID, TargetLanguage: run.TargetLanguage, Channel: ChannelWeChat, DestinationRef: "primary-wechat", Status: AutomaticTurnSettlementQueued, CreatedAt: now, UpdatedAt: now},
			IdempotencyKey: "auto:final_turn:turn-1:wechat:primary-wechat",
		}},
	}
	pool := &automaticPostgresPoolFake{tx: &automaticTxFake{}}
	repository := &PostgresRepository{pool: pool}
	if err := repository.ScheduleAutomaticTurn(t.Context(), record); err != nil {
		t.Fatalf("ScheduleAutomaticTurn() error = %v", err)
	}

	claimPool := &automaticPostgresPoolFake{tx: &automaticTxFake{
		row: []*automaticRowFake{{values: automaticRunValues(run)}},
	}}
	claimRepository := &PostgresRepository{pool: claimPool}
	claimedRun, claimed, err := claimRepository.ClaimAutomaticTurnFallback(t.Context(), run.AccountID, run.TurnID)
	if err != nil || !claimed || claimedRun.Status != AutomaticTurnRunFallbackPending {
		t.Fatalf("ClaimAutomaticTurnFallback() = %#v, %v, claimed=%v", claimedRun, err, claimed)
	}
	if err := (&PostgresRepository{pool: &automaticPostgresPoolFake{execTag: pgconn.NewCommandTag("UPDATE 1")}}).MarkAutomaticTurnFallbackPlayed(t.Context(), run.AccountID, run.TurnID); err != nil {
		t.Fatalf("MarkAutomaticTurnFallbackPlayed() error = %v", err)
	}
	if err := (&PostgresRepository{pool: &automaticPostgresPoolFake{execTag: pgconn.NewCommandTag("UPDATE 1")}}).MarkAutomaticTurnRestored(t.Context(), run.AccountID, run.TurnID); err != nil {
		t.Fatalf("MarkAutomaticTurnRestored() error = %v", err)
	}
}

func TestAutomaticPostgresScheduleIsIdempotentAndDetectsConflict(t *testing.T) {
	run := testAutomaticRun()
	run.Status = AutomaticTurnRunPending
	run.TargetCount = 0
	record := AutomaticTurnScheduleRecord{Run: run}
	matching := &automaticPostgresPoolFake{tx: &automaticTxFake{
		execTags: []pgconn.CommandTag{pgconn.NewCommandTag("INSERT 0 0")},
		row:      []*automaticRowFake{{values: automaticRunValues(run)}},
	}}
	if err := (&PostgresRepository{pool: matching}).ScheduleAutomaticTurn(t.Context(), record); err != nil {
		t.Fatalf("matching replay ScheduleAutomaticTurn() error = %v", err)
	}
	conflict := run
	conflict.SessionID = "other-session"
	conflictPool := &automaticPostgresPoolFake{tx: &automaticTxFake{
		execTags: []pgconn.CommandTag{pgconn.NewCommandTag("INSERT 0 0")},
		row:      []*automaticRowFake{{values: automaticRunValues(conflict)}},
	}}
	if err := (&PostgresRepository{pool: conflictPool}).ScheduleAutomaticTurn(t.Context(), record); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting replay ScheduleAutomaticTurn() error = %v, want conflict", err)
	}
}

func TestAutomaticPostgresFallbackMarksAreIdempotent(t *testing.T) {
	run := testAutomaticRun()
	playedPool := &automaticPostgresPoolFake{
		execTag: pgconn.NewCommandTag("UPDATE 0"),
		row:     []*automaticRowFake{{values: []any{AutomaticTurnRunFallbackPlayed}}},
	}
	if err := (&PostgresRepository{pool: playedPool}).MarkAutomaticTurnFallbackPlayed(t.Context(), run.AccountID, run.TurnID); err != nil {
		t.Fatalf("replayed fallback mark error = %v", err)
	}
	restoredPool := &automaticPostgresPoolFake{
		execTag: pgconn.NewCommandTag("UPDATE 0"),
		row:     []*automaticRowFake{{values: []any{AutomaticTurnRunRestored}}},
	}
	if err := (&PostgresRepository{pool: restoredPool}).MarkAutomaticTurnRestored(t.Context(), run.AccountID, run.TurnID); err != nil {
		t.Fatalf("replayed restore mark error = %v", err)
	}
	conflictPlayed := &automaticPostgresPoolFake{
		execTag: pgconn.NewCommandTag("UPDATE 0"),
		row:     []*automaticRowFake{{values: []any{AutomaticTurnRunFailed}}},
	}
	if err := (&PostgresRepository{pool: conflictPlayed}).MarkAutomaticTurnFallbackPlayed(t.Context(), run.AccountID, run.TurnID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting fallback mark error = %v, want conflict", err)
	}
}

func TestAutomaticPostgresReadQueriesPropagateDatabaseErrors(t *testing.T) {
	rowsErr := errors.New("rows unavailable")
	if _, err := (&PostgresRepository{pool: &automaticPostgresPoolFake{rows: []*automaticRowsFake{{err: rowsErr}}}}).ListAutomaticTurnRetryCandidates(t.Context(), 1); !errors.Is(err, rowsErr) {
		t.Fatalf("rows error = %v, want %v", err, rowsErr)
	}
	if _, err := (&PostgresRepository{pool: &automaticPostgresPoolFake{rows: []*automaticRowsFake{{rows: [][]any{{"bad"}}}}}}).ListAutomaticTurnRestoreCandidates(t.Context(), 1); err == nil {
		t.Fatal("scan error = nil, want scan failure")
	}
	rowErr := errors.New("row unavailable")
	if _, err := (&PostgresRepository{pool: &automaticPostgresPoolFake{row: []*automaticRowFake{{err: rowErr}}}}).GetAutomaticTurnRun(t.Context(), "account-1", "turn-1"); !errors.Is(err, rowErr) {
		t.Fatalf("row error = %v, want %v", err, rowErr)
	}
}

func TestAutomaticPostgresSchedulePropagatesTransactionErrors(t *testing.T) {
	run := testAutomaticRun()
	run.TargetCount = 0
	beginErr := errors.New("begin unavailable")
	if err := (&PostgresRepository{pool: &automaticPostgresPoolFake{beginErr: beginErr}}).ScheduleAutomaticTurn(t.Context(), AutomaticTurnScheduleRecord{Run: run}); !errors.Is(err, beginErr) {
		t.Fatalf("begin error = %v, want %v", err, beginErr)
	}
	execErr := errors.New("insert unavailable")
	if err := (&PostgresRepository{pool: &automaticPostgresPoolFake{tx: &automaticTxFake{execErr: execErr}}}).ScheduleAutomaticTurn(t.Context(), AutomaticTurnScheduleRecord{Run: run}); !errors.Is(err, execErr) {
		t.Fatalf("insert error = %v, want %v", err, execErr)
	}
}

func TestAutomaticPostgresRetrySkipsQueuedTarget(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	pool := &automaticPostgresPoolFake{
		tx: &automaticTxFake{row: []*automaticRowFake{
			{values: []any{AutomaticTurnRunPartiallySucceeded, 1}},
			{values: []any{AutomaticTurnSettlementQueued}},
		}},
		row: []*automaticRowFake{{values: []any{"message-1", "account-1", ChannelWeChat, "primary-wechat", 1, []byte(`[]`), MessageStatusRetrying, 1, (*string)(nil), now, now}}},
	}
	message, err := (&PostgresRepository{pool: pool}).RetryAutomaticTurnTarget(t.Context(), "account-1", "turn-1", "message-1", "retry-key")
	if err != nil || message.Status != MessageStatusRetrying {
		t.Fatalf("queued retry = %#v, %v", message, err)
	}
}

func TestAutomaticPostgresRejectsUnsupportedRetryAndRestoreStates(t *testing.T) {
	if NewPostgresRepository(nil) == nil {
		t.Fatal("NewPostgresRepository(nil) returned nil")
	}
	run := testAutomaticRun()
	restorePool := &automaticPostgresPoolFake{
		execTag: pgconn.NewCommandTag("UPDATE 0"),
		row:     []*automaticRowFake{{values: []any{AutomaticTurnRunFallbackPending}}},
	}
	if err := (&PostgresRepository{pool: restorePool}).MarkAutomaticTurnRestored(t.Context(), run.AccountID, run.TurnID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conflicting restore mark error = %v, want conflict", err)
	}
	retryPool := &automaticPostgresPoolFake{tx: &automaticTxFake{row: []*automaticRowFake{
		{values: []any{AutomaticTurnRunPartiallySucceeded, 1}},
		{values: []any{AutomaticTurnSettlementFailed}},
		{values: []any{"account-1", ChannelWeChat, "primary-wechat", MessageStatusFailed, maxAutomaticTargetAttempts, (*string)(nil)}},
	}}}
	if _, err := (&PostgresRepository{pool: retryPool}).RetryAutomaticTurnTarget(t.Context(), run.AccountID, run.TurnID, "message-1", "retry-key"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("exhausted retry error = %v, want conflict", err)
	}
	updateErr := errors.New("state update unavailable")
	if err := (&PostgresRepository{pool: &automaticPostgresPoolFake{execErr: updateErr}}).MarkAutomaticTurnFallbackPlayed(t.Context(), run.AccountID, run.TurnID); !errors.Is(err, updateErr) {
		t.Fatalf("fallback update error = %v, want %v", err, updateErr)
	}
	if err := (&PostgresRepository{pool: &automaticPostgresPoolFake{execErr: updateErr}}).MarkAutomaticTurnRestored(t.Context(), run.AccountID, run.TurnID); !errors.Is(err, updateErr) {
		t.Fatalf("restore update error = %v, want %v", err, updateErr)
	}
}

func TestAutomaticPostgresFallbackClaimHandlesPendingAndMissingRows(t *testing.T) {
	run := testAutomaticRun()
	pending := run
	pending.Status = AutomaticTurnRunFallbackPending
	pending.UpdatedAt = time.Now().UTC()
	pool := &automaticPostgresPoolFake{tx: &automaticTxFake{row: []*automaticRowFake{{values: automaticRunValues(pending)}}}}
	claimedRun, claimed, err := (&PostgresRepository{pool: pool}).ClaimAutomaticTurnFallback(t.Context(), run.AccountID, run.TurnID)
	if err != nil || claimed || claimedRun.Status != AutomaticTurnRunFallbackPending {
		t.Fatalf("pending ClaimAutomaticTurnFallback() = %#v, %v, claimed=%v", claimedRun, err, claimed)
	}

	missingPool := &automaticPostgresPoolFake{execTag: pgconn.NewCommandTag("UPDATE 0"), row: []*automaticRowFake{{err: pgx.ErrNoRows}}}
	if err := (&PostgresRepository{pool: missingPool}).MarkAutomaticTurnFallbackPlayed(t.Context(), run.AccountID, run.TurnID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing MarkAutomaticTurnFallbackPlayed() error = %v, want not found", err)
	}
}

func TestAutomaticPostgresSettlementRefreshesRun(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	tx := &automaticTxFake{row: []*automaticRowFake{
		{values: []any{"account-1", "turn-1"}},
		{values: []any{1, 1, 1, 0}},
	}}
	if err := settleAutomaticTurnTarget(t.Context(), tx, "message-1", AttemptStatusSucceeded, nil, now); err != nil {
		t.Fatalf("settleAutomaticTurnTarget() error = %v", err)
	}
	if err := settleAutomaticTurnTarget(t.Context(), &automaticTxFake{row: []*automaticRowFake{{err: pgx.ErrNoRows}}}, "message-1", AttemptStatusFailed, nil, now); err != nil {
		t.Fatalf("missing settlement error = %v", err)
	}
}

func TestAutomaticPostgresRetryTargetQueuesAttemptAndRefreshesRun(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	messageID := "message-1"
	tx := &automaticTxFake{row: []*automaticRowFake{
		{values: []any{AutomaticTurnRunPartiallySucceeded, 1}},
		{values: []any{AutomaticTurnSettlementFailed}},
		{values: []any{"account-1", ChannelWeChat, "primary-wechat", MessageStatusFailed, 1, (*string)(nil)}},
		{err: pgx.ErrNoRows},
		{values: []any{1, 0, 0, 0}},
	}}
	pool := &automaticPostgresPoolFake{
		tx:  tx,
		row: []*automaticRowFake{{values: []any{messageID, "account-1", ChannelWeChat, "primary-wechat", 1, []byte(`[]`), MessageStatusRetrying, 2, (*string)(nil), now, now}}},
	}
	message, err := (&PostgresRepository{pool: pool}).RetryAutomaticTurnTarget(t.Context(), "account-1", "turn-1", messageID, "retry-key")
	if err != nil {
		t.Fatalf("RetryAutomaticTurnTarget() error = %v", err)
	}
	if message.ID != messageID || message.Status != MessageStatusRetrying || message.Attempts != 2 {
		t.Fatalf("retried message = %#v", message)
	}

	conflictPool := &automaticPostgresPoolFake{tx: &automaticTxFake{row: []*automaticRowFake{
		{values: []any{AutomaticTurnRunPartiallySucceeded, 1}},
		{values: []any{AutomaticTurnSettlementSucceeded}},
	}}}
	if _, err := (&PostgresRepository{pool: conflictPool}).RetryAutomaticTurnTarget(t.Context(), "account-1", "turn-1", messageID, "retry-key"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("succeeded target retry error = %v, want conflict", err)
	}
}

func TestAutomaticPostgresRetryRejectsTotalFailureRun(t *testing.T) {
	pool := &automaticPostgresPoolFake{tx: &automaticTxFake{row: []*automaticRowFake{{values: []any{AutomaticTurnRunFailed, 0}}}}}
	if _, err := (&PostgresRepository{pool: pool}).RetryAutomaticTurnTarget(t.Context(), "account-1", "turn-1", "message-1", "retry-key"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("RetryAutomaticTurnTarget() error = %v, want conflict", err)
	}
}

func testAutomaticRun() AutomaticTurnRun {
	now := time.Unix(1_700_000_000, 0).UTC()
	return AutomaticTurnRun{
		AccountID: "account-1", TurnID: "turn-1", SessionID: "session-1", TraceID: "trace-1",
		TargetLanguage: "zh-CN", TranslatedText: "译文", LanguageConfigVersion: 3,
		Status: AutomaticTurnRunFailed, TargetCount: 1, SettledCount: 1, FailedCount: 1,
		FallbackOperationID: "fallback-turn-1", CreatedAt: now, UpdatedAt: now,
	}
}

func automaticRunValues(run AutomaticTurnRun) []any {
	return []any{run.AccountID, run.TurnID, run.SessionID, run.TraceID, run.TargetLanguage, run.TranslatedText, run.LanguageConfigVersion, run.Status, run.TargetCount, run.SettledCount, run.SucceededCount, run.FailedCount, run.FallbackOperationID, run.CreatedAt, run.UpdatedAt}
}

func automaticSettlementValues(settlement AutomaticTurnSettlement) []any {
	return []any{settlement.AccountID, settlement.TurnID, settlement.SessionID, settlement.TargetLanguage, settlement.Channel, settlement.DestinationRef, settlement.Status, settlement.MessageID, settlement.ErrorCode, settlement.CreatedAt, settlement.UpdatedAt}
}

type automaticPostgresPoolFake struct {
	beginErr error
	execErr  error
	execTag  pgconn.CommandTag
	row      []*automaticRowFake
	rows     []*automaticRowsFake
	queries  []string
	tx       *automaticTxFake
}

func (p *automaticPostgresPoolFake) Begin(context.Context) (pgx.Tx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	if p.tx == nil {
		p.tx = &automaticTxFake{}
	}
	return p.tx, nil
}

func (p *automaticPostgresPoolFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	if p.execErr != nil {
		return pgconn.CommandTag{}, p.execErr
	}
	if p.execTag == (pgconn.CommandTag{}) {
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return p.execTag, nil
}

func (p *automaticPostgresPoolFake) Query(_ context.Context, query string, _ ...any) (pgx.Rows, error) {
	p.queries = append(p.queries, query)
	if len(p.rows) == 0 {
		return &automaticRowsFake{}, nil
	}
	result := p.rows[0]
	p.rows = p.rows[1:]
	return result, nil
}

func (p *automaticPostgresPoolFake) QueryRow(context.Context, string, ...any) pgx.Row {
	if len(p.row) == 0 {
		return &automaticRowFake{err: pgx.ErrNoRows}
	}
	result := p.row[0]
	p.row = p.row[1:]
	return result
}

type automaticTxFake struct {
	row      []*automaticRowFake
	execTags []pgconn.CommandTag
	execErr  error
}

func (t *automaticTxFake) Begin(context.Context) (pgx.Tx, error) { return t, nil }
func (t *automaticTxFake) Commit(context.Context) error          { return nil }
func (t *automaticTxFake) Rollback(context.Context) error        { return nil }
func (t *automaticTxFake) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (t *automaticTxFake) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (t *automaticTxFake) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (t *automaticTxFake) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (t *automaticTxFake) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	if t.execErr != nil {
		return pgconn.CommandTag{}, t.execErr
	}
	if len(t.execTags) > 0 {
		result := t.execTags[0]
		t.execTags = t.execTags[1:]
		return result, nil
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (t *automaticTxFake) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return &automaticRowsFake{}, nil
}
func (t *automaticTxFake) QueryRow(context.Context, string, ...any) pgx.Row {
	if len(t.row) == 0 {
		return &automaticRowFake{err: pgx.ErrNoRows}
	}
	result := t.row[0]
	t.row = t.row[1:]
	return result
}
func (t *automaticTxFake) Conn() *pgx.Conn { return nil }

type automaticRowFake struct {
	values []any
	err    error
}

func (r *automaticRowFake) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(r.values) != len(dest) {
		return errors.New("scan column count mismatch")
	}
	for i, value := range r.values {
		if i >= len(dest) || value == nil {
			continue
		}
		target := reflect.ValueOf(dest[i])
		if target.Kind() != reflect.Pointer || target.IsNil() {
			return errors.New("scan destination must be a non-nil pointer")
		}
		target = target.Elem()
		source := reflect.ValueOf(value)
		if source.Type().AssignableTo(target.Type()) {
			target.Set(source)
			continue
		}
		if source.Type().ConvertibleTo(target.Type()) {
			target.Set(source.Convert(target.Type()))
			continue
		}
		return errors.New("scan value type mismatch")
	}
	return nil
}

type automaticRowsFake struct {
	rows   [][]any
	index  int
	closed bool
	err    error
}

func (r *automaticRowsFake) Close()                                       { r.closed = true }
func (r *automaticRowsFake) Err() error                                   { return r.err }
func (r *automaticRowsFake) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *automaticRowsFake) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *automaticRowsFake) Next() bool {
	if r.index >= len(r.rows) {
		r.closed = true
		return false
	}
	r.index++
	return true
}
func (r *automaticRowsFake) Scan(dest ...any) error {
	if r.index == 0 || r.index > len(r.rows) {
		return errors.New("scan called before Next")
	}
	return (&automaticRowFake{values: r.rows[r.index-1]}).Scan(dest...)
}
func (r *automaticRowsFake) Values() ([]any, error) { return nil, nil }
func (r *automaticRowsFake) RawValues() [][]byte    { return nil }
func (r *automaticRowsFake) Conn() *pgx.Conn        { return nil }

var _ postgresPool = (*automaticPostgresPoolFake)(nil)
var _ pgx.Tx = (*automaticTxFake)(nil)
var _ pgx.Rows = (*automaticRowsFake)(nil)
var _ pgx.Row = (*automaticRowFake)(nil)
