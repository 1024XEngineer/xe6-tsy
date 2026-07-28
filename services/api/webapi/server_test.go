package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func TestListParticipantsUsesTrustedAccountContext(t *testing.T) {
	nextCursor := "cursor_02"
	participantRepository := &participantRepository{listResponse: recordsv1.ParticipantListResponse{
		Items:      []recordsv1.Participant{{ID: "p_01", SessionID: "vs_01", SpeakerCode: "speaker_01"}},
		CursorPage: recordsv1.CursorPage{NextCursor: &nextCursor},
	}}
	handler := newHandler(t, participantRepository, &turnRepository{}, "acct_01")

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_01/participants", nil)
	unauthenticated.Header.Set("X-Account-ID", "acct_01")
	assertError(t, serve(handler, unauthenticated), http.StatusUnauthorized, recordsv1.ErrorUnauthenticated)

	request := accountRequest(http.MethodGet, "/api/v1/voice-sessions/vs_01/participants?cursor=cursor_01&limit=2", nil, "acct_01", false)
	response := serve(handler, request)
	if response.Code != http.StatusOK {
		t.Fatalf("list participants status = %d, body = %s", response.Code, response.Body.String())
	}
	var body recordsv1.ParticipantListResponse
	decodeBody(t, response, &body)
	if len(body.Items) != 1 || body.Items[0].ID != "p_01" || body.NextCursor == nil || *body.NextCursor != nextCursor {
		t.Fatalf("list participants body = %#v", body)
	}
	if participantRepository.listQuery.Cursor != "cursor_01" || participantRepository.listQuery.Limit != 2 {
		t.Fatalf("list participants query = %#v", participantRepository.listQuery)
	}

	crossAccount := accountRequest(http.MethodGet, "/api/v1/voice-sessions/vs_01/participants", nil, "acct_02", false)
	assertError(t, serve(handler, crossAccount), http.StatusForbidden, recordsv1.ErrorForbidden)

	invalidLimit := accountRequest(http.MethodGet, "/api/v1/voice-sessions/vs_01/participants?limit=101", nil, "acct_01", false)
	assertError(t, serve(handler, invalidLimit), http.StatusBadRequest, recordsv1.ErrorInvalidRequest)
}

func TestUpdateParticipantRequiresSystemAndKeepsExplicitNull(t *testing.T) {
	participantRepository := &participantRepository{updated: recordsv1.Participant{ID: "p_01", SessionID: "vs_01"}}
	handler := newHandler(t, participantRepository, &turnRepository{}, "acct_01")
	path := "/api/v1/voice-sessions/vs_01/participants/p_01"

	notSystem := accountRequest(http.MethodPatch, path, strings.NewReader(`{"voice_profile_id":null}`), "acct_01", false)
	assertError(t, serve(handler, notSystem), http.StatusForbidden, recordsv1.ErrorForbidden)

	request := accountRequest(http.MethodPatch, path, strings.NewReader(`{"voice_profile_id":null}`), "acct_01", true)
	response := serve(handler, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update participant status = %d, body = %s", response.Code, response.Body.String())
	}
	if !participantRepository.update.VoiceProfileIDSet || participantRepository.update.VoiceProfileID != nil {
		t.Fatalf("participant update = %#v, want explicit null", participantRepository.update)
	}
	var body recordsv1.Participant
	decodeBody(t, response, &body)
	if body.ID != "p_01" {
		t.Fatalf("update participant body = %#v", body)
	}

	unknownField := accountRequest(http.MethodPatch, path, strings.NewReader(`{"corrected_by":"client"}`), "acct_01", true)
	assertError(t, serve(handler, unknownField), http.StatusBadRequest, recordsv1.ErrorInvalidRequest)
}

func TestTurnReadRoutesUseContractShapesAndFilters(t *testing.T) {
	nextCursor := "turn_cursor_02"
	turn := recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01", SourceText: "source", TranslatedText: "translation"}
	turnRepository := &turnRepository{
		turn: turn,
		listResponse: recordsv1.VoiceTurnListResponse{
			Items:      []recordsv1.VoiceTurn{turn},
			CursorPage: recordsv1.CursorPage{NextCursor: &nextCursor},
		},
		historyResponse: recordsv1.VoiceTurnListResponse{Items: []recordsv1.VoiceTurn{turn}},
	}
	handler := newHandler(t, &participantRepository{}, turnRepository, "acct_01")

	listRequest := accountRequest(http.MethodGet, "/api/v1/voice-sessions/vs_01/turns?participant_id=p_01&speaker_code=speaker_01&attribution_status=pending&source_language=en-US&target_language=zh-CN&limit=4", nil, "acct_01", false)
	listResponse := serve(handler, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list turns status = %d, body = %s", listResponse.Code, listResponse.Body.String())
	}
	var listBody recordsv1.VoiceTurnListResponse
	decodeBody(t, listResponse, &listBody)
	if len(listBody.Items) != 1 || listBody.Items[0].ID != "vt_01" || listBody.NextCursor == nil || *listBody.NextCursor != nextCursor {
		t.Fatalf("list turns body = %#v", listBody)
	}
	if turnRepository.listQuery.SessionID != "vs_01" || turnRepository.listQuery.ParticipantID != "p_01" || turnRepository.listQuery.AttributionStatus != recordsv1.AttributionPending || turnRepository.listQuery.Limit != 4 {
		t.Fatalf("list turns query = %#v", turnRepository.listQuery)
	}

	getResponse := serve(handler, accountRequest(http.MethodGet, "/api/v1/voice-turns/vt_01", nil, "acct_01", false))
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get turn status = %d, body = %s", getResponse.Code, getResponse.Body.String())
	}
	var getBody recordsv1.VoiceTurn
	decodeBody(t, getResponse, &getBody)
	if getBody.ID != "vt_01" {
		t.Fatalf("get turn body = %#v", getBody)
	}

	historyResponse := serve(handler, accountRequest(http.MethodGet, "/api/v1/translation-history?created_from=2026-07-24T08:00:00Z&created_to=2026-07-24T09:00:00Z", nil, "acct_01", false))
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("history status = %d, body = %s", historyResponse.Code, historyResponse.Body.String())
	}
	if turnRepository.historyAccountID != "acct_01" || turnRepository.historyQuery.CreatedFrom == nil || turnRepository.historyQuery.CreatedTo == nil {
		t.Fatalf("history scope/query = %q %#v", turnRepository.historyAccountID, turnRepository.historyQuery)
	}

	accountQuery := accountRequest(http.MethodGet, "/api/v1/translation-history?account_id=acct_02", nil, "acct_01", false)
	assertError(t, serve(handler, accountQuery), http.StatusBadRequest, recordsv1.ErrorInvalidRequest)
}

func TestCorrectAttributionRequiresSystemAndValidTarget(t *testing.T) {
	turnRepository := &turnRepository{
		turn:                 recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01", SourceText: "source", TranslatedText: "translation"},
		participantInSession: true,
	}
	handler := newHandler(t, &participantRepository{}, turnRepository, "acct_01")
	path := "/api/v1/voice-turns/vt_01/attribution"
	body := `{"participant_id":"p_01","attribution_status":"corrected","speaker_confidence":0.8}`

	notSystem := accountRequest(http.MethodPatch, path, strings.NewReader(body), "acct_01", false)
	assertError(t, serve(handler, notSystem), http.StatusForbidden, recordsv1.ErrorForbidden)

	valid := accountRequest(http.MethodPatch, path, strings.NewReader(body), "acct_01", true)
	response := serve(handler, valid)
	if response.Code != http.StatusOK {
		t.Fatalf("correct attribution status = %d, body = %s", response.Code, response.Body.String())
	}
	if turnRepository.attributionUpdate.CorrectedBy != recordsv1.CorrectedBySystem || turnRepository.attributionUpdate.CorrectedAt.IsZero() {
		t.Fatalf("attribution update = %#v", turnRepository.attributionUpdate)
	}
	var corrected recordsv1.VoiceTurn
	decodeBody(t, response, &corrected)
	if corrected.CorrectedBy == nil || *corrected.CorrectedBy != recordsv1.CorrectedBySystem || corrected.SourceText != "source" || corrected.TranslatedText != "translation" {
		t.Fatalf("corrected turn = %#v", corrected)
	}

	invalidStatus := accountRequest(http.MethodPatch, path, strings.NewReader(`{"participant_id":"p_01","attribution_status":"pending"}`), "acct_01", true)
	assertError(t, serve(handler, invalidStatus), http.StatusBadRequest, recordsv1.ErrorInvalidAttribution)

	turnRepository.participantInSession = false
	mismatchedParticipant := accountRequest(http.MethodPatch, path, strings.NewReader(`{"participant_id":"p_02","attribution_status":"confirmed"}`), "acct_01", true)
	assertError(t, serve(handler, mismatchedParticipant), http.StatusBadRequest, recordsv1.ErrorInvalidAttribution)
}

func TestErrorMappingReturnsNotFoundAndInternalError(t *testing.T) {
	turnRepository := &turnRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, findErr: turns.ErrTurnNotFound}
	handler := newHandler(t, &participantRepository{}, turnRepository, "acct_01")
	request := accountRequest(http.MethodGet, "/api/v1/voice-turns/vt_01", nil, "acct_01", false)
	assertError(t, serve(handler, request), http.StatusNotFound, recordsv1.ErrorVoiceTurnAbsent)

	turnRepository.findErr = errors.New("database unavailable")
	response := serve(handler, accountRequest(http.MethodGet, "/api/v1/voice-turns/vt_01", nil, "acct_01", false))
	assertError(t, response, http.StatusInternalServerError, recordsv1.ErrorInternal)
	var body recordsv1.ErrorResponse
	decodeBody(t, response, &body)
	if body.Error.Message != "internal server error" {
		t.Fatalf("internal error message = %q, want generic message", body.Error.Message)
	}
}

func TestInternalErrorLogsCauseWithoutExposingIt(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	turnRepository := &turnRepository{
		turn:    recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"},
		findErr: errors.New("dial postgres password=secret table=voice_turns"),
	}
	handler := newHandlerWithLogger(t, &participantRepository{}, turnRepository, "acct_01", logger)
	response := serve(handler, accountRequest(http.MethodGet, "/api/v1/voice-turns/vt_01", nil, "acct_01", false))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), "password=secret") {
		t.Fatalf("response exposed internal error: %s", response.Body.String())
	}
	if !strings.Contains(logs.String(), "password=secret") {
		t.Fatalf("logger did not record internal error: %s", logs.String())
	}
}

func newHandler(t *testing.T, participantRepository *participantRepository, turnRepository *turnRepository, ownerID string) http.Handler {
	t.Helper()
	return newHandlerWithLogger(t, participantRepository, turnRepository, ownerID, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newHandlerWithLogger(t *testing.T, participantRepository *participantRepository, turnRepository *turnRepository, ownerID string, logger *slog.Logger) http.Handler {
	t.Helper()
	owners := sessionOwners{ownerID: ownerID}
	participantService := participants.NewService(participantRepository, owners, func() time.Time {
		return time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	})
	turnService := turns.NewService(turnRepository, owners, func() time.Time {
		return time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	})
	handler := NewHandler(Dependencies{
		Participants: participantService,
		Turns:        turnService,
		Accounts:     ContextAccountProvider{},
		System:       ContextSystemAuthorizer{},
		Logger:       logger,
	})
	mux := http.NewServeMux()
	handler.Register(mux)
	return mux
}

func accountRequest(method, target string, body *strings.Reader, accountID string, system bool) *http.Request {
	ctx := WithAccountID(context.Background(), accountID)
	if system {
		ctx = WithSystemActor(ctx)
	}
	var reader io.Reader
	if body != nil {
		reader = body
	}
	return httptest.NewRequest(method, target, reader).WithContext(ctx)
}

func serve(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode recordsv1.ErrorCode) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, wantStatus, response.Body.String())
	}
	var body recordsv1.ErrorResponse
	decodeBody(t, response, &body)
	if body.Error.Code != wantCode || body.Error.RequestID == "" {
		t.Fatalf("error body = %#v, want code %q and request ID", body, wantCode)
	}
}

func decodeBody(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

type participantRepository struct {
	listResponse recordsv1.ParticipantListResponse
	listQuery    recordsv1.ListParticipantsQuery
	updated      recordsv1.Participant
	update       participants.Update
}

func (r *participantRepository) List(_ context.Context, _ string, query recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error) {
	r.listQuery = query
	return r.listResponse, nil
}

func (r *participantRepository) Update(_ context.Context, _ string, _ string, update participants.Update) (recordsv1.Participant, error) {
	r.update = update
	return r.updated, nil
}

func (r *participantRepository) FindOrCreate(context.Context, recordsv1.SpeakerObservation) (recordsv1.Participant, error) {
	return recordsv1.Participant{}, nil
}

type turnRepository struct {
	turn                 recordsv1.VoiceTurn
	findErr              error
	listResponse         recordsv1.VoiceTurnListResponse
	listQuery            recordsv1.ListTurnsQuery
	historyResponse      recordsv1.VoiceTurnListResponse
	historyAccountID     string
	historyQuery         recordsv1.ListTurnsQuery
	participantInSession bool
	attributionUpdate    turns.AttributionUpdate
}

func (*turnRepository) StoreFinalTurn(context.Context, recordsv1.FinalTurnEvent) error {
	return nil
}

func (r *turnRepository) ListSession(_ context.Context, _ string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	r.listQuery = query
	return r.listResponse, nil
}

func (r *turnRepository) Find(context.Context, string) (recordsv1.VoiceTurn, error) {
	if r.findErr != nil {
		return recordsv1.VoiceTurn{}, r.findErr
	}
	return r.turn, nil
}

func (r *turnRepository) ListHistory(_ context.Context, accountID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	r.historyAccountID = accountID
	r.historyQuery = query
	return r.historyResponse, nil
}

func (r *turnRepository) CorrectAttribution(_ context.Context, update turns.AttributionUpdate) (recordsv1.VoiceTurn, error) {
	if !r.participantInSession {
		return recordsv1.VoiceTurn{}, turns.ErrInvalidAttribution
	}
	r.attributionUpdate = update
	updated := r.turn
	updated.ParticipantID = &update.ParticipantID
	updated.AttributionStatus = update.AttributionStatus
	updated.SpeakerConfidence = update.SpeakerConfidence
	updated.CorrectedBy = &update.CorrectedBy
	updated.CorrectedAt = &update.CorrectedAt
	return updated, nil
}

func (*turnRepository) ReadFinalTurns(context.Context, string, []string) ([]recordsv1.FinalTurnSnapshot, error) {
	return nil, nil
}

type sessionOwners struct {
	ownerID string
}

func (owners sessionOwners) AccountIDForSession(context.Context, string) (string, error) {
	return owners.ownerID, nil
}
