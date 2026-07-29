//go:build integration

package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecordsPhase4PersistenceAndDelivery(t *testing.T) {
	fixture := newRecordsPhase4Fixture(t)
	produced := fixture.publishFinalTurns(t)

	var storedTurns int
	if err := fixture.pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM voice_turns").Scan(&storedTurns); err != nil {
		t.Fatalf("count stored turns: %v", err)
	}
	if storedTurns != 3 {
		t.Fatalf("stored turns = %d, want 3", storedTurns)
	}

	ownerID := fixture.owner.ID
	history, err := fixture.records.Turns.ListHistory(t.Context(), ownerID, recordsv1.ListTurnsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListHistory() error = %v", err)
	}
	assertPhase4TurnIDs(t, history.Items, produced.attributed.ID, produced.pending.ID)

	pendingRecord, err := fixture.records.Turns.Get(t.Context(), ownerID, produced.pending.ID)
	if err != nil {
		t.Fatalf("Get(pending) error = %v", err)
	}
	if pendingRecord.ParticipantID != nil || pendingRecord.DisplayName != nil {
		t.Fatalf("pending turn attribution = %#v", pendingRecord)
	}
	attributedRecord, err := fixture.records.Turns.Get(t.Context(), ownerID, produced.attributed.ID)
	if err != nil {
		t.Fatalf("Get(attributed) error = %v", err)
	}
	if attributedRecord.ParticipantID == nil || *attributedRecord.ParticipantID != fixture.attributedParticipant.ID || attributedRecord.DisplayName == nil || *attributedRecord.DisplayName != fixture.attributedName {
		t.Fatalf("attributed turn = %#v", attributedRecord)
	}

	deliveryReader := delivery.NewRecordsTurnReader(fixture.records.FinalTurns)
	snapshots, err := deliveryReader.ReadFinalTurns(t.Context(), ownerID, []string{produced.attributed.ID, produced.pending.ID})
	if err != nil {
		t.Fatalf("delivery ReadFinalTurns() error = %v", err)
	}
	if len(snapshots) != 2 || snapshots[0].TurnID != produced.attributed.ID || snapshots[1].TurnID != produced.pending.ID {
		t.Fatalf("delivery snapshots = %#v", snapshots)
	}
	if snapshots[0].ParticipantID == nil || *snapshots[0].ParticipantID != fixture.attributedParticipant.ID || snapshots[0].SpeakerLabelSnapshot == nil || *snapshots[0].SpeakerLabelSnapshot != fixture.attributedName {
		t.Fatalf("attributed delivery snapshot = %#v", snapshots[0])
	}
	if snapshots[1].ParticipantID != nil || snapshots[1].SpeakerLabelSnapshot != nil {
		t.Fatalf("pending delivery snapshot = %#v", snapshots[1])
	}

	for _, turnIDs := range [][]string{
		{produced.attributed.ID, produced.foreign.ID},
		{produced.attributed.ID, "phase4_missing_turn"},
	} {
		batch, err := deliveryReader.ReadFinalTurns(t.Context(), ownerID, turnIDs)
		if !errors.Is(err, turns.ErrTurnNotFound) {
			t.Fatalf("ReadFinalTurns(%v) error = %v, want not found", turnIDs, err)
		}
		if batch != nil {
			t.Fatalf("ReadFinalTurns(%v) snapshots = %#v, want nil", turnIDs, batch)
		}
	}
}

type recordsPhase4Fixture struct {
	pool                  *pgxpool.Pool
	records               *recordstore.ServiceComposition
	owner                 accounts.Account
	foreign               accounts.Account
	ownerSession          string
	foreignSession        string
	attributedParticipant recordsv1.Participant
	attributedName        string
	currentTime           time.Time
	producer              *pipeline.PipelineService
}

type recordsPhase4Turns struct {
	pending    pipeline.TurnContext
	attributed pipeline.TurnContext
	foreign    pipeline.TurnContext
}

func newRecordsPhase4Fixture(t *testing.T) *recordsPhase4Fixture {
	t.Helper()
	databaseURL := recordsHTTPTestDatabaseURL(t)
	pool, err := recordstore.Open(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("recordstore.Open() error = %v", err)
	}
	t.Cleanup(pool.Close)
	if err := recordstore.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("recordstore.Migrate() error = %v", err)
	}

	accountRepository := accounts.NewPostgresRepository(pool)
	owner, err := accountRepository.CreateAnonymous(t.Context())
	if err != nil {
		t.Fatalf("create owner account: %v", err)
	}
	foreign, err := accountRepository.CreateAnonymous(t.Context())
	if err != nil {
		t.Fatalf("create foreign account: %v", err)
	}
	const (
		ownerSession   = "phase4_owner_session"
		foreignSession = "phase4_foreign_session"
	)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO voice_sessions (id, account_id, status, audio_config, capabilities) VALUES
			($1, $2, 'created', '{}'::jsonb, '{}'::jsonb),
			($3, $4, 'created', '{}'::jsonb, '{}'::jsonb)`,
		ownerSession,
		owner.ID,
		foreignSession,
		foreign.ID,
	); err != nil {
		t.Fatalf("insert phase4 sessions: %v", err)
	}

	sessionScope, err := recordstore.NewPostgresSessionScopeReader(pool)
	if err != nil {
		t.Fatalf("NewPostgresSessionScopeReader() error = %v", err)
	}
	recordsServices, err := recordstore.NewServices(
		pool,
		[]byte("phase4-records-cursor-signing-key"),
		recordstore.NewCanonicalSessionOwner(accountRepository),
		sessionScope,
	)
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}

	participantWriter := recordstore.NewParticipantWriter(pool)
	attributedParticipant, err := participantWriter.FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         ownerSession,
		TurnID:            "phase4_attributed_turn",
		ProviderSpeakerID: "speaker_a",
	})
	if err != nil {
		t.Fatalf("create attributed participant: %v", err)
	}
	attributedName := "Speaker A"
	if _, err := participantWriter.Update(t.Context(), ownerSession, attributedParticipant.ID, participants.Update{
		DisplayName:    &attributedName,
		DisplayNameSet: true,
		UpdatedAt:      time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("name attributed participant: %v", err)
	}

	fixture := &recordsPhase4Fixture{
		pool:                  pool,
		records:               recordsServices,
		owner:                 owner,
		foreign:               foreign,
		ownerSession:          ownerSession,
		foreignSession:        foreignSession,
		attributedParticipant: attributedParticipant,
		attributedName:        attributedName,
		currentTime:           time.Date(2026, 7, 29, 10, 1, 0, 0, time.UTC),
	}
	fixture.producer = pipeline.NewPipelineService(pipeline.PipelineDependencies{
		Translator: &translate.FakeProvider{Result: translate.Result{
			Text:     "translated text",
			Provider: "integration-translator",
			Model:    "integration-model",
		}},
		TTS: tts.NewFakeProvider(tts.FakeProviderConfig{Result: tts.Result{
			Provider: "integration-tts",
			Model:    "integration-tts-model",
		}}),
		Speakers:   recordsServices.Participants,
		FinalTurns: pipeline.NewPostgresFinalTurnSink(pool),
		Usage:      phase4UsageSink{},
		Audio:      phase4AudioSink{},
		Runtime:    phase4RuntimeReporter{},
		Now:        func() time.Time { return fixture.currentTime },
	})
	return fixture
}

func (f *recordsPhase4Fixture) publishFinalTurns(t *testing.T) recordsPhase4Turns {
	t.Helper()
	workerContext, cancelWorker := context.WithCancel(t.Context())
	defer cancelWorker()
	workerSource := newPhase4AckSource(recordstore.NewFinalTurnOutbox(f.pool), 3, cancelWorker)
	worker := turns.NewFinalTurnWorker(workerSource, turns.NewFinalTurnHandler(f.records.Turns))
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerContext) }()

	result := recordsPhase4Turns{
		pending: phase4Turn(
			f.ownerSession,
			f.owner.ID,
			"phase4_pending_turn",
			"trace_pending",
			1,
			f.currentTime.Add(-time.Second),
		),
	}
	publishPhase4Turn(t, f.producer, result.pending, asr.FinalResult{
		Text: "pending source", SourceLanguage: "zh-CN", Provider: "integration-asr", Model: "integration-asr-model",
		AudioDuration: time.Second,
	})

	f.currentTime = f.currentTime.Add(2 * time.Second)
	result.attributed = phase4Turn(
		f.ownerSession,
		f.owner.ID,
		"phase4_attributed_turn",
		"trace_attributed",
		2,
		f.currentTime.Add(-time.Second),
	)
	publishPhase4Turn(t, f.producer, result.attributed, asr.FinalResult{
		Text: "attributed source", SourceLanguage: "zh-CN", Provider: "integration-asr", Model: "integration-asr-model",
		ProviderSpeakerID: "speaker_a", AudioDuration: time.Second,
	})

	f.currentTime = f.currentTime.Add(2 * time.Second)
	result.foreign = phase4Turn(
		f.foreignSession,
		f.foreign.ID,
		"phase4_foreign_turn",
		"trace_foreign",
		1,
		f.currentTime.Add(-time.Second),
	)
	publishPhase4Turn(t, f.producer, result.foreign, asr.FinalResult{
		Text: "foreign source", SourceLanguage: "zh-CN", Provider: "integration-asr", Model: "integration-asr-model",
		AudioDuration: time.Second,
	})

	if err := <-workerDone; err != nil {
		t.Fatalf("final turn worker error = %v", err)
	}
	return result
}

func phase4Turn(sessionID, accountID, turnID, traceID string, sequenceNo int64, startedAt time.Time) pipeline.TurnContext {
	return pipeline.TurnContext{
		ID: turnID, SessionID: sessionID, AccountID: accountID, TraceID: traceID, SequenceNo: sequenceNo,
		LanguageConfig: session.LanguageConfigSnapshot{
			SessionID: sessionID, Version: 1, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		},
		StartedAt: startedAt,
	}
}

func publishPhase4Turn(t *testing.T, producer *pipeline.PipelineService, turn pipeline.TurnContext, result asr.FinalResult) {
	t.Helper()
	if err := producer.HandleASRFinal(t.Context(), turn, result); err != nil {
		t.Fatalf("HandleASRFinal(%q) error = %v", turn.ID, err)
	}
}

func assertPhase4TurnIDs(t *testing.T, turns []recordsv1.VoiceTurn, want ...string) {
	t.Helper()
	if len(turns) != len(want) {
		t.Fatalf("turn count = %d, want %d: %#v", len(turns), len(want), turns)
	}
	for index, turnID := range want {
		if turns[index].ID != turnID {
			t.Fatalf("turn[%d] ID = %q, want %q", index, turns[index].ID, turnID)
		}
	}
}

type phase4AckSource struct {
	source    turns.FinalTurnDeliverySource
	remaining atomic.Int32
	cancel    context.CancelFunc
}

func newPhase4AckSource(source turns.FinalTurnDeliverySource, count int, cancel context.CancelFunc) *phase4AckSource {
	result := &phase4AckSource{source: source, cancel: cancel}
	result.remaining.Store(int32(count))
	return result
}

func (s *phase4AckSource) Receive(ctx context.Context) (turns.FinalTurnDelivery, error) {
	delivery, err := s.source.Receive(ctx)
	if err != nil {
		return nil, err
	}
	return &phase4AckDelivery{FinalTurnDelivery: delivery, source: s}, nil
}

type phase4AckDelivery struct {
	turns.FinalTurnDelivery
	source *phase4AckSource
}

func (d *phase4AckDelivery) Ack() error {
	if err := d.FinalTurnDelivery.Ack(); err != nil {
		return err
	}
	if d.source.remaining.Add(-1) == 0 {
		d.source.cancel()
	}
	return nil
}

type phase4UsageSink struct{}

func (phase4UsageSink) Publish(context.Context, pipeline.UsageFact) error { return nil }

type phase4AudioSink struct{}

func (phase4AudioSink) Publish(context.Context, pipeline.AudioChunk) error { return nil }

type phase4RuntimeReporter struct{}

func (phase4RuntimeReporter) SetProcessingState(context.Context, session.ProcessingStateUpdate) error {
	return nil
}

var _ pipeline.UsageFactSink = phase4UsageSink{}
var _ pipeline.AudioChunkSink = phase4AudioSink{}
var _ session.RuntimeStateReporter = phase4RuntimeReporter{}
var _ turns.FinalTurnDeliverySource = (*phase4AckSource)(nil)
