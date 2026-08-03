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

func TestRecordsPhase4HTTPAndSnapshotConsistency(t *testing.T) {
	fixture := newRecordsPhase4Fixture(t)
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
		TurnID:            "phase4_correction_turn",
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
	assertPhase4CorrectedTurn(t, corrected, correctedParticipant.ID, confidence)
	assertPhase4ImmutableTurn(t, pendingBefore, corrected)

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
	assertPhase4ImmutableDeliverySnapshot(t, initialSnapshots[0], correctedSnapshots[0])

	history, err := fixture.records.Turns.ListHistory(t.Context(), ownerID, recordsv1.ListTurnsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListHistory() after correction error = %v", err)
	}
	assertPhase4TurnIDs(t, history.Items, produced.attributed.ID, produced.pending.ID)
	assertPhase4CorrectedTurn(t, history.Items[1], correctedParticipant.ID, confidence)
	assertPhase4ImmutableTurn(t, pendingBefore, history.Items[1])

	handler := buildMux(
		languages.NewHandler(nil, nil),
		nil,
		fixture.dependencies.handler,
		fixture.dependencies.accounts,
		fixture.dependencies.tokens,
	)
	historyResponse := phase4HTTPRequest(handler, http.MethodGet, "/api/v1/translation-history?limit=20", fixture.ownerAccessToken, "")
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("history HTTP status = %d, want %d, body = %s", historyResponse.Code, http.StatusOK, historyResponse.Body.String())
	}
	var historyBody recordsv1.VoiceTurnListResponse
	decodePhase4JSON(t, historyResponse, &historyBody)
	assertPhase4TurnIDs(t, historyBody.Items, produced.attributed.ID, produced.pending.ID)
	assertPhase4CorrectedTurn(t, historyBody.Items[1], correctedParticipant.ID, confidence)
	assertPhase4ImmutableTurn(t, pendingBefore, historyBody.Items[1])

	turnResponse := phase4HTTPRequest(handler, http.MethodGet, "/api/v1/voice-turns/"+produced.pending.ID, fixture.ownerAccessToken, "")
	if turnResponse.Code != http.StatusOK {
		t.Fatalf("single turn HTTP status = %d, want %d, body = %s", turnResponse.Code, http.StatusOK, turnResponse.Body.String())
	}
	var turnBody recordsv1.VoiceTurn
	decodePhase4JSON(t, turnResponse, &turnBody)
	assertPhase4CorrectedTurn(t, turnBody, correctedParticipant.ID, confidence)
	assertPhase4ImmutableTurn(t, pendingBefore, turnBody)

	sessionTurnsResponse := phase4HTTPRequest(handler, http.MethodGet, "/api/v1/voice-sessions/"+fixture.ownerSession+"/turns?limit=20", fixture.ownerAccessToken, "")
	if sessionTurnsResponse.Code != http.StatusOK {
		t.Fatalf("session turns HTTP status = %d, want %d, body = %s", sessionTurnsResponse.Code, http.StatusOK, sessionTurnsResponse.Body.String())
	}
	var sessionTurnsBody recordsv1.VoiceTurnListResponse
	decodePhase4JSON(t, sessionTurnsResponse, &sessionTurnsBody)
	assertPhase4TurnIDs(t, sessionTurnsBody.Items, produced.pending.ID, produced.attributed.ID)

	participantsResponse := phase4HTTPRequest(handler, http.MethodGet, "/api/v1/voice-sessions/"+fixture.ownerSession+"/participants?limit=20", fixture.ownerAccessToken, "")
	if participantsResponse.Code != http.StatusOK {
		t.Fatalf("participants HTTP status = %d, want %d, body = %s", participantsResponse.Code, http.StatusOK, participantsResponse.Body.String())
	}
	var participantsBody recordsv1.ParticipantListResponse
	decodePhase4JSON(t, participantsResponse, &participantsBody)
	if len(participantsBody.Items) != 2 {
		t.Fatalf("participants HTTP count = %d, want 2: %#v", len(participantsBody.Items), participantsBody.Items)
	}

	foreignSessionResponse := phase4HTTPRequest(handler, http.MethodGet, "/api/v1/voice-sessions/"+fixture.foreignSession+"/turns?limit=20", fixture.ownerAccessToken, "")
	assertPhase4Error(t, foreignSessionResponse, http.StatusForbidden, recordsv1.ErrorForbidden)
	foreignTurnResponse := phase4HTTPRequest(handler, http.MethodGet, "/api/v1/voice-turns/"+produced.foreign.ID, fixture.ownerAccessToken, "")
	assertPhase4Error(t, foreignTurnResponse, http.StatusNotFound, recordsv1.ErrorVoiceTurnAbsent)

	participantPatchResponse := phase4HTTPRequest(
		handler,
		http.MethodPatch,
		"/api/v1/voice-sessions/"+fixture.ownerSession+"/participants/"+correctedParticipant.ID,
		fixture.ownerAccessToken,
		`{"display_name":"renamed"}`,
	)
	assertPhase4Error(t, participantPatchResponse, http.StatusForbidden, recordsv1.ErrorForbidden)
	attributionPatchResponse := phase4HTTPRequest(
		handler,
		http.MethodPatch,
		"/api/v1/voice-turns/"+produced.pending.ID+"/attribution",
		fixture.ownerAccessToken,
		`{"participant_id":"`+correctedParticipant.ID+`","attribution_status":"corrected"}`,
	)
	assertPhase4Error(t, attributionPatchResponse, http.StatusForbidden, recordsv1.ErrorForbidden)
}

type recordsPhase4Fixture struct {
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

type recordsPhase4Turns struct {
	pending    pipeline.TurnContext
	attributed pipeline.TurnContext
	foreign    pipeline.TurnContext
}

type phase4SpeakerReader struct {
	delegate recordsv1.SpeakerAttributionReader
}

func (r phase4SpeakerReader) GetProvisionalAttribution(ctx context.Context, observation recordsv1.SpeakerObservation) (recordsv1.SpeakerAttribution, error) {
	if observation.ProviderSpeakerID == "local-mic" {
		return recordsv1.SpeakerAttribution{
			SpeakerCode:       recordsv1.PendingSpeakerCode,
			AttributionStatus: recordsv1.AttributionPending,
		}, nil
	}
	return r.delegate.GetProvisionalAttribution(ctx, observation)
}

func newRecordsPhase4Fixture(t *testing.T) *recordsPhase4Fixture {
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
		ownerSession   = "phase4_owner_session"
		foreignSession = "phase4_foreign_session"
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
		Speakers:       phase4SpeakerReader{delegate: recordsServices.Participants},
		FinalTurns:     pipeline.NewPostgresFinalTurnSink(pool),
		Usage:          phase4UsageSink{},
		Audio:          phase4AudioSink{},
		Runtime:        phase4RuntimeReporter{},
		SpeakerTimeout: 5 * time.Second,
		Now:            func() time.Time { return fixture.currentTime },
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

func assertPhase4CorrectedTurn(t *testing.T, turn recordsv1.VoiceTurn, participantID string, confidence float64) {
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

func assertPhase4ImmutableTurn(t *testing.T, before, after recordsv1.VoiceTurn) {
	t.Helper()
	if before.ID != after.ID || before.SessionID != after.SessionID || before.SequenceNo != after.SequenceNo ||
		before.SourceLanguage != after.SourceLanguage || before.TargetLanguage != after.TargetLanguage || before.LanguageConfigVersion != after.LanguageConfigVersion ||
		before.SourceText != after.SourceText || before.TranslatedText != after.TranslatedText ||
		!before.StartedAt.Equal(after.StartedAt) || !before.EndedAt.Equal(after.EndedAt) || !before.CreatedAt.Equal(after.CreatedAt) {
		t.Fatalf("immutable turn fields changed: before=%#v after=%#v", before, after)
	}
}

func equalPhase4StringPointers(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func assertPhase4ImmutableDeliverySnapshot(t *testing.T, before, after delivery.FinalTurnSnapshot) {
	t.Helper()
	if before.TurnID != after.TurnID || before.SessionID != after.SessionID || before.SourceLanguage != after.SourceLanguage || before.TargetLanguage != after.TargetLanguage ||
		before.LanguageConfigVersion != after.LanguageConfigVersion || before.SourceText != after.SourceText || before.TranslatedText != after.TranslatedText || !before.CreatedAt.Equal(after.CreatedAt) {
		t.Fatalf("immutable delivery snapshot fields changed: before=%#v after=%#v", before, after)
	}
}

func phase4HTTPRequest(handler http.Handler, method, target, accessToken, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodePhase4JSON(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode HTTP response %q: %v", response.Body.String(), err)
	}
}

func assertPhase4Error(t *testing.T, response *httptest.ResponseRecorder, status int, code recordsv1.ErrorCode) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("HTTP status = %d, want %d, body = %s", response.Code, status, response.Body.String())
	}
	var body recordsv1.ErrorResponse
	decodePhase4JSON(t, response, &body)
	if body.Error.Code != code {
		t.Fatalf("HTTP error code = %q, want %q", body.Error.Code, code)
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
