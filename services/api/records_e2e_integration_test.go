//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
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

func TestRecordsPersistenceAndDelivery(t *testing.T) {
	fixture := newRecordsFixture(t)
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
	assertRecordsTurnIDs(t, history.Items, produced.attributed.ID, produced.pending.ID)

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
		{produced.attributed.ID, "records_missing_turn"},
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

func TestRecordsHTTPAndSnapshotConsistency(t *testing.T) {
	fixture := newRecordsFixture(t)
	produced := fixture.publishFinalTurns(t)
	ownerID := fixture.owner.ID

	pendingBefore, err := fixture.records.Turns.Get(t.Context(), ownerID, produced.pending.ID)
	if err != nil {
		t.Fatalf("Get(pending) before correction error = %v", err)
	}
	deliveryReader := delivery.NewRecordsTurnReader(fixture.records.FinalTurns)
	initialSnapshots, err := deliveryReader.ReadFinalTurns(t.Context(), ownerID, []string{produced.pending.ID})
	if err != nil {
		t.Fatalf("initial delivery ReadFinalTurns() error = %v", err)
	}
	if len(initialSnapshots) != 1 || initialSnapshots[0].ParticipantID != nil || initialSnapshots[0].SpeakerLabelSnapshot != nil {
		t.Fatalf("initial pending delivery snapshot = %#v", initialSnapshots)
	}

	participantWriter := recordstore.NewParticipantWriter(fixture.pool)
	correctedParticipant, err := participantWriter.FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         fixture.ownerSession,
		TurnID:            "records_correction_turn",
		ProviderSpeakerID: "speaker_b",
	})
	if err != nil {
		t.Fatalf("create correction participant: %v", err)
	}
	correctedName := "Speaker B"
	if _, err := participantWriter.Update(t.Context(), fixture.ownerSession, correctedParticipant.ID, participants.Update{
		DisplayName:    &correctedName,
		DisplayNameSet: true,
		UpdatedAt:      time.Date(2026, 7, 29, 10, 6, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("name correction participant: %v", err)
	}

	confidence := 0.93
	corrected, err := fixture.records.Turns.CorrectAttribution(t.Context(), ownerID, produced.pending.ID, recordsv1.UpdateAttributionRequest{
		ParticipantID:     correctedParticipant.ID,
		AttributionStatus: recordsv1.AttributionCorrected,
		SpeakerConfidence: &confidence,
	}, true)
	if err != nil {
		t.Fatalf("CorrectAttribution() error = %v", err)
	}
	assertRecordsCorrectedTurn(t, corrected, correctedParticipant.ID, confidence)
	assertRecordsImmutableTurn(t, pendingBefore, corrected)

	correctedSnapshots, err := deliveryReader.ReadFinalTurns(t.Context(), ownerID, []string{produced.pending.ID})
	if err != nil {
		t.Fatalf("corrected delivery ReadFinalTurns() error = %v", err)
	}
	if len(correctedSnapshots) != 1 || correctedSnapshots[0].ParticipantID == nil || *correctedSnapshots[0].ParticipantID != correctedParticipant.ID {
		t.Fatalf("corrected delivery snapshot = %#v", correctedSnapshots)
	}
	if correctedSnapshots[0].SpeakerLabelSnapshot == nil || *correctedSnapshots[0].SpeakerLabelSnapshot != correctedName {
		t.Fatalf("corrected delivery speaker label = %#v, want %q", correctedSnapshots[0].SpeakerLabelSnapshot, correctedName)
	}
	if initialSnapshots[0].ParticipantID != nil || initialSnapshots[0].SpeakerLabelSnapshot != nil {
		t.Fatalf("initial delivery snapshot changed after correction = %#v", initialSnapshots[0])
	}
	assertRecordsImmutableDeliverySnapshot(t, initialSnapshots[0], correctedSnapshots[0])

	history, err := fixture.records.Turns.ListHistory(t.Context(), ownerID, recordsv1.ListTurnsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListHistory() after correction error = %v", err)
	}
	assertRecordsTurnIDs(t, history.Items, produced.attributed.ID, produced.pending.ID)
	assertRecordsCorrectedTurn(t, history.Items[1], correctedParticipant.ID, confidence)
	assertRecordsImmutableTurn(t, pendingBefore, history.Items[1])

	handler := buildMux(
		languages.NewHandler(nil, nil),
		nil,
		fixture.dependencies.handler,
		fixture.dependencies.accounts,
		fixture.dependencies.tokens,
	)
	historyResponse := recordsHTTPRequest(handler, http.MethodGet, "/api/v1/translation-history?limit=20", fixture.ownerAccessToken, "")
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("history HTTP status = %d, want %d, body = %s", historyResponse.Code, http.StatusOK, historyResponse.Body.String())
	}
	var historyBody recordsv1.VoiceTurnListResponse
	decodeRecordsJSON(t, historyResponse, &historyBody)
	assertRecordsTurnIDs(t, historyBody.Items, produced.attributed.ID, produced.pending.ID)
	assertRecordsCorrectedTurn(t, historyBody.Items[1], correctedParticipant.ID, confidence)
	assertRecordsImmutableTurn(t, pendingBefore, historyBody.Items[1])

	turnResponse := recordsHTTPRequest(handler, http.MethodGet, "/api/v1/voice-turns/"+produced.pending.ID, fixture.ownerAccessToken, "")
	if turnResponse.Code != http.StatusOK {
		t.Fatalf("single turn HTTP status = %d, want %d, body = %s", turnResponse.Code, http.StatusOK, turnResponse.Body.String())
	}
	var turnBody recordsv1.VoiceTurn
	decodeRecordsJSON(t, turnResponse, &turnBody)
	assertRecordsCorrectedTurn(t, turnBody, correctedParticipant.ID, confidence)
	assertRecordsImmutableTurn(t, pendingBefore, turnBody)

	sessionTurnsResponse := recordsHTTPRequest(handler, http.MethodGet, "/api/v1/voice-sessions/"+fixture.ownerSession+"/turns?limit=20", fixture.ownerAccessToken, "")
	if sessionTurnsResponse.Code != http.StatusOK {
		t.Fatalf("session turns HTTP status = %d, want %d, body = %s", sessionTurnsResponse.Code, http.StatusOK, sessionTurnsResponse.Body.String())
	}
	var sessionTurnsBody recordsv1.VoiceTurnListResponse
	decodeRecordsJSON(t, sessionTurnsResponse, &sessionTurnsBody)
	assertRecordsTurnIDs(t, sessionTurnsBody.Items, produced.pending.ID, produced.attributed.ID)

	participantsResponse := recordsHTTPRequest(handler, http.MethodGet, "/api/v1/voice-sessions/"+fixture.ownerSession+"/participants?limit=20", fixture.ownerAccessToken, "")
	if participantsResponse.Code != http.StatusOK {
		t.Fatalf("participants HTTP status = %d, want %d, body = %s", participantsResponse.Code, http.StatusOK, participantsResponse.Body.String())
	}
	var participantsBody recordsv1.ParticipantListResponse
	decodeRecordsJSON(t, participantsResponse, &participantsBody)
	if len(participantsBody.Items) != 2 {
		t.Fatalf("participants HTTP count = %d, want 2: %#v", len(participantsBody.Items), participantsBody.Items)
	}

	foreignSessionResponse := recordsHTTPRequest(handler, http.MethodGet, "/api/v1/voice-sessions/"+fixture.foreignSession+"/turns?limit=20", fixture.ownerAccessToken, "")
	assertRecordsError(t, foreignSessionResponse, http.StatusForbidden, recordsv1.ErrorForbidden)
	foreignTurnResponse := recordsHTTPRequest(handler, http.MethodGet, "/api/v1/voice-turns/"+produced.foreign.ID, fixture.ownerAccessToken, "")
	assertRecordsError(t, foreignTurnResponse, http.StatusNotFound, recordsv1.ErrorVoiceTurnAbsent)

	participantPatchResponse := recordsHTTPRequest(
		handler,
		http.MethodPatch,
		"/api/v1/voice-sessions/"+fixture.ownerSession+"/participants/"+correctedParticipant.ID,
		fixture.ownerAccessToken,
		`{"display_name":"renamed"}`,
	)
	assertRecordsError(t, participantPatchResponse, http.StatusForbidden, recordsv1.ErrorForbidden)
	attributionPatchResponse := recordsHTTPRequest(
		handler,
		http.MethodPatch,
		"/api/v1/voice-turns/"+produced.pending.ID+"/attribution",
		fixture.ownerAccessToken,
		`{"participant_id":"`+correctedParticipant.ID+`","attribution_status":"corrected"}`,
	)
	assertRecordsError(t, attributionPatchResponse, http.StatusForbidden, recordsv1.ErrorForbidden)
}

type recordsFixture struct {
	pool                  *pgxpool.Pool
	dependencies          *recordsHTTPDependencies
	records               *recordstore.ServiceComposition
	owner                 accounts.Account
	foreign               accounts.Account
	ownerAccessToken      string
	ownerSession          string
	foreignSession        string
	attributedParticipant recordsv1.Participant
	attributedName        string
	currentTime           time.Time
	producer              *pipeline.PipelineService
}

type recordsTurns struct {
	pending    pipeline.TurnContext
	attributed pipeline.TurnContext
	foreign    pipeline.TurnContext
}

func newRecordsFixture(t *testing.T) *recordsFixture {
	t.Helper()
	databaseURL := recordsHTTPTestDatabaseURL(t)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("JWT_SECRET", strings.Repeat("s", 36))

	dependencies, err := newRecordsHTTPDependencies(t.Context())
	if err != nil {
		t.Fatalf("newRecordsHTTPDependencies() error = %v", err)
	}
	t.Cleanup(dependencies.cleanup)

	pool, err := recordstore.Open(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("recordstore.Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	accountRepository := accounts.NewPostgresRepository(pool)
	owner, err := dependencies.accounts.CreateAnonymous(t.Context())
	if err != nil {
		t.Fatalf("create owner account: %v", err)
	}
	foreign, err := dependencies.accounts.CreateAnonymous(t.Context())
	if err != nil {
		t.Fatalf("create foreign account: %v", err)
	}
	const (
		ownerSession   = "records_owner_session"
		foreignSession = "records_foreign_session"
	)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO voice_sessions (id, account_id, status, audio_config, capabilities) VALUES
			($1, $2, 'created', '{}'::jsonb, '{}'::jsonb),
			($3, $4, 'created', '{}'::jsonb, '{}'::jsonb)`,
		ownerSession,
		owner.Account.ID,
		foreignSession,
		foreign.Account.ID,
	); err != nil {
		t.Fatalf("insert records sessions: %v", err)
	}

	sessionScope, err := recordstore.NewPostgresSessionScopeReader(pool)
	if err != nil {
		t.Fatalf("NewPostgresSessionScopeReader() error = %v", err)
	}
	recordsServices, err := recordstore.NewServices(
		pool,
		[]byte("records-cursor-signing-key"),
		recordstore.NewCanonicalSessionOwner(accountRepository),
		sessionScope,
	)
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}

	participantWriter := recordstore.NewParticipantWriter(pool)
	attributedParticipant, err := participantWriter.FindOrCreate(t.Context(), recordsv1.SpeakerObservation{
		SessionID:         ownerSession,
		TurnID:            "records_attributed_turn",
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

	fixture := &recordsFixture{
		pool:                  pool,
		dependencies:          dependencies,
		records:               recordsServices,
		owner:                 owner.Account,
		foreign:               foreign.Account,
		ownerAccessToken:      owner.Tokens.AccessToken,
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
		FinalTurns: pipeline.NewPostgresFinalTurnSink(pool),
		FinalGate:  finalTurnGate{},
		Usage:      recordsUsageSink{},
		Audio:      recordsAudioSink{},
		Runtime:    recordsRuntimeReporter{},
		Now:        func() time.Time { return fixture.currentTime },
	})
	return fixture
}

func (f *recordsFixture) publishFinalTurns(t *testing.T) recordsTurns {
	t.Helper()
	workerContext, cancelWorker := context.WithCancel(t.Context())
	defer cancelWorker()
	workerSource := newRecordsAckSource(recordstore.NewFinalTurnOutbox(f.pool), 3, cancelWorker)
	worker := turns.NewFinalTurnWorker(workerSource, turns.NewFinalTurnHandler(f.records.Turns))
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerContext) }()

	result := recordsTurns{
		pending: recordsTurn(
			f.ownerSession,
			f.owner.ID,
			"records_pending_turn",
			"trace_pending",
			1,
			f.currentTime.Add(-time.Second),
		),
	}
	publishRecordsTurn(t, f.producer, result.pending, asr.FinalResult{
		Text: "pending source", SourceLanguage: "zh-CN", Provider: "integration-asr", Model: "integration-asr-model",
		AudioDuration: time.Second,
	})

	f.currentTime = f.currentTime.Add(2 * time.Second)
	result.attributed = recordsTurn(
		f.ownerSession,
		f.owner.ID,
		"records_attributed_turn",
		"trace_attributed",
		2,
		f.currentTime.Add(-time.Second),
	)
	publishRecordsTurn(t, f.producer, result.attributed, asr.FinalResult{
		Text: "attributed source", SourceLanguage: "zh-CN", Provider: "integration-asr", Model: "integration-asr-model",
		ProviderSpeakerID: "speaker_a", AudioDuration: time.Second,
	})

	f.currentTime = f.currentTime.Add(2 * time.Second)
	result.foreign = recordsTurn(
		f.foreignSession,
		f.foreign.ID,
		"records_foreign_turn",
		"trace_foreign",
		1,
		f.currentTime.Add(-time.Second),
	)
	publishRecordsTurn(t, f.producer, result.foreign, asr.FinalResult{
		Text: "foreign source", SourceLanguage: "zh-CN", Provider: "integration-asr", Model: "integration-asr-model",
		AudioDuration: time.Second,
	})

	if err := <-workerDone; err != nil {
		t.Fatalf("final turn worker error = %v", err)
	}

	// The realtime producer never writes participant rows; the API attribution worker owns the
	// mapping. Only the turn with provider evidence is enqueued; the other two remain pending.
	f.resolveAttributionTask(t)
	return result
}

func (f *recordsFixture) resolveAttributionTask(t *testing.T) {
	t.Helper()
	var taskCount int
	if err := f.pool.QueryRow(t.Context(), "SELECT COUNT(*) FROM attribution_tasks").Scan(&taskCount); err != nil {
		t.Fatalf("count attribution tasks: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("attribution tasks = %d, want 1 with provider evidence", taskCount)
	}

	store := recordstore.NewAttributionTaskStore(f.pool)
	delivery, err := store.Receive(t.Context())
	if err != nil {
		t.Fatalf("receive attribution task: %v", err)
	}
	if err := f.records.AttributionWorker.Process(t.Context(), delivery); err != nil {
		t.Fatalf("attribution worker Process() error = %v", err)
	}
}

func recordsTurn(sessionID, accountID, turnID, traceID string, sequenceNo int64, startedAt time.Time) pipeline.TurnContext {
	return pipeline.TurnContext{
		ID: turnID, SessionID: sessionID, AccountID: accountID, TraceID: traceID, SequenceNo: sequenceNo,
		LanguageConfig: session.LanguageConfigSnapshot{
			SessionID: sessionID, Version: 1, Status: "active",
			LanguagePairs: []session.LanguagePair{{Source: "zh-CN", Target: "en-US"}},
		},
		StartedAt: startedAt,
	}
}

func publishRecordsTurn(t *testing.T, producer *pipeline.PipelineService, turn pipeline.TurnContext, result asr.FinalResult) {
	t.Helper()
	if err := producer.HandleASRFinal(t.Context(), turn, result); err != nil {
		t.Fatalf("HandleASRFinal(%q) error = %v", turn.ID, err)
	}
}

func assertRecordsTurnIDs(t *testing.T, turns []recordsv1.VoiceTurn, want ...string) {
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

func assertRecordsCorrectedTurn(t *testing.T, turn recordsv1.VoiceTurn, participantID string, confidence float64) {
	t.Helper()
	if turn.ParticipantID == nil || *turn.ParticipantID != participantID {
		t.Fatalf("corrected participant_id = %#v, want %q", turn.ParticipantID, participantID)
	}
	if turn.AttributionStatus != recordsv1.AttributionCorrected {
		t.Fatalf("corrected attribution_status = %q, want %q", turn.AttributionStatus, recordsv1.AttributionCorrected)
	}
	if turn.SpeakerConfidence == nil || *turn.SpeakerConfidence != confidence {
		t.Fatalf("corrected speaker_confidence = %#v, want %v", turn.SpeakerConfidence, confidence)
	}
	if turn.CorrectedBy == nil || *turn.CorrectedBy != recordsv1.CorrectedBySystem {
		t.Fatalf("corrected corrected_by = %#v, want %q", turn.CorrectedBy, recordsv1.CorrectedBySystem)
	}
	if turn.CorrectedAt == nil {
		t.Fatal("corrected corrected_at = nil")
	}
}

func assertRecordsImmutableTurn(t *testing.T, before, after recordsv1.VoiceTurn) {
	t.Helper()
	if before.ID != after.ID || before.SessionID != after.SessionID || before.SequenceNo != after.SequenceNo ||
		before.SourceLanguage != after.SourceLanguage || before.TargetLanguage != after.TargetLanguage || before.LanguageConfigVersion != after.LanguageConfigVersion ||
		before.SourceText != after.SourceText || before.TranslatedText != after.TranslatedText ||
		!before.StartedAt.Equal(after.StartedAt) || !before.EndedAt.Equal(after.EndedAt) || !before.CreatedAt.Equal(after.CreatedAt) {
		t.Fatalf("immutable turn fields changed: before=%#v after=%#v", before, after)
	}
}

func equalRecordsStringPointers(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func assertRecordsImmutableDeliverySnapshot(t *testing.T, before, after delivery.FinalTurnSnapshot) {
	t.Helper()
	if before.TurnID != after.TurnID || before.SessionID != after.SessionID || before.SourceLanguage != after.SourceLanguage || before.TargetLanguage != after.TargetLanguage ||
		before.LanguageConfigVersion != after.LanguageConfigVersion || before.SourceText != after.SourceText || before.TranslatedText != after.TranslatedText || !before.CreatedAt.Equal(after.CreatedAt) {
		t.Fatalf("immutable delivery snapshot fields changed: before=%#v after=%#v", before, after)
	}
}

func recordsHTTPRequest(handler http.Handler, method, target, accessToken, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeRecordsJSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode HTTP response %q: %v", response.Body.String(), err)
	}
}

func assertRecordsError(t *testing.T, response *httptest.ResponseRecorder, status int, code recordsv1.ErrorCode) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("HTTP status = %d, want %d, body = %s", response.Code, status, response.Body.String())
	}
	var body recordsv1.ErrorResponse
	decodeRecordsJSON(t, response, &body)
	if body.Error.Code != code {
		t.Fatalf("HTTP error code = %q, want %q", body.Error.Code, code)
	}
}

type recordsAckSource struct {
	source    turns.FinalTurnDeliverySource
	remaining atomic.Int32
	cancel    context.CancelFunc
}

func newRecordsAckSource(source turns.FinalTurnDeliverySource, count int, cancel context.CancelFunc) *recordsAckSource {
	result := &recordsAckSource{source: source, cancel: cancel}
	result.remaining.Store(int32(count))
	return result
}

func (s *recordsAckSource) Receive(ctx context.Context) (turns.FinalTurnDelivery, error) {
	delivery, err := s.source.Receive(ctx)
	if err != nil {
		return nil, err
	}
	return &recordsAckDelivery{FinalTurnDelivery: delivery, source: s}, nil
}

type recordsAckDelivery struct {
	turns.FinalTurnDelivery
	source *recordsAckSource
}

func (d *recordsAckDelivery) Ack() error {
	if err := d.FinalTurnDelivery.Ack(); err != nil {
		return err
	}
	if d.source.remaining.Add(-1) == 0 {
		d.source.cancel()
	}
	return nil
}

type recordsUsageSink struct{}

type finalTurnGate struct{}

func (finalTurnGate) CommitFinalTurn(ctx context.Context, _ pipeline.TurnContext, commit pipeline.FinalTurnCommit) (bool, error) {
	if err := commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (recordsUsageSink) Publish(context.Context, pipeline.UsageFact) error { return nil }

type recordsAudioSink struct{}

func (recordsAudioSink) Publish(context.Context, pipeline.AudioChunk) error { return nil }

type recordsRuntimeReporter struct{}

func (recordsRuntimeReporter) SetProcessingState(context.Context, session.ProcessingStateUpdate) error {
	return nil
}

var _ pipeline.UsageFactSink = recordsUsageSink{}
var _ pipeline.AudioChunkSink = recordsAudioSink{}
var _ session.RuntimeStateReporter = recordsRuntimeReporter{}
var _ turns.FinalTurnDeliverySource = (*recordsAckSource)(nil)
