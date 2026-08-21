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
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

func TestAuthenticateValidatesBearerTokenAndStopsOnFailure(t *testing.T) {
	server := newTestServer(t)
	type testCase struct {
		name          string
		authorization string
		claims        accounts.AccessTokenClaims
		verifyErr     error
		wantStatus    int
		wantAccount   string
		wantVerify    bool
	}
	tests := []testCase{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", authorization: "Basic access-token", wantStatus: http.StatusUnauthorized},
		{name: "missing token", authorization: "Bearer", wantStatus: http.StatusUnauthorized},
		{name: "multiple tokens", authorization: "Bearer access-token extra", wantStatus: http.StatusUnauthorized},
		{name: "verifier rejects", authorization: "Bearer rejected", verifyErr: errors.New("expired"), wantStatus: http.StatusUnauthorized, wantVerify: true},
		{name: "empty account claim", authorization: "Bearer empty", wantStatus: http.StatusUnauthorized, wantVerify: true},
		{name: "valid case insensitive scheme", authorization: "bEaReR access-token", claims: accounts.AccessTokenClaims{AccountID: "acct_verified"}, wantStatus: http.StatusNoContent, wantAccount: "acct_verified", wantVerify: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := &accessTokenVerifier{claims: test.claims, err: test.verifyErr}
			nextCalls := 0
			next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				nextCalls++
				accountID, ok := ContextAccountProvider{}.AccountID(request.Context())
				if !ok || accountID != test.wantAccount {
					t.Fatalf("account context = %q, %v; want %q, true", accountID, ok, test.wantAccount)
				}
				writer.WriteHeader(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", test.authorization)
			response := serve(server.Authenticate(verifier, next), request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if nextCalls != boolToInt(test.wantStatus == http.StatusNoContent) {
				t.Fatalf("next calls = %d, want %d", nextCalls, boolToInt(test.wantStatus == http.StatusNoContent))
			}
			if (verifier.calls > 0) != test.wantVerify {
				t.Fatalf("verifier calls = %d, want called = %v", verifier.calls, test.wantVerify)
			}
		})
	}
}

func TestNewHandlerAndRegisterFailFastOnMissingDependencies(t *testing.T) {
	valid := validDependencies()
	for _, test := range []struct {
		name string
		edit func(*Dependencies)
	}{
		{name: "participants", edit: func(dependencies *Dependencies) { dependencies.Participants = nil }},
		{name: "turns", edit: func(dependencies *Dependencies) { dependencies.Turns = nil }},
		{name: "accounts", edit: func(dependencies *Dependencies) { dependencies.Accounts = nil }},
		{name: "system", edit: func(dependencies *Dependencies) { dependencies.System = nil }},
		{name: "logger", edit: func(dependencies *Dependencies) { dependencies.Logger = nil }},
	} {
		t.Run("NewHandler/"+test.name, func(t *testing.T) {
			dependencies := valid
			test.edit(&dependencies)
			assertPanics(t, func() { NewHandler(dependencies) })
		})
	}

	server := NewHandler(valid)
	for _, test := range []struct {
		name string
		edit func(*RouteMiddleware)
	}{
		{name: "account middleware", edit: func(middleware *RouteMiddleware) { middleware.Account = nil }},
		{name: "system middleware", edit: func(middleware *RouteMiddleware) { middleware.System = nil }},
	} {
		t.Run("Register/"+test.name, func(t *testing.T) {
			middleware := RouteMiddleware{
				Account: func(next http.Handler) http.Handler { return next },
				System:  func(next http.Handler) http.Handler { return next },
			}
			test.edit(&middleware)
			assertPanics(t, func() { server.Register(http.NewServeMux(), middleware) })
		})
	}
}

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

func TestUpdateParticipantMapsConflictTo409(t *testing.T) {
	participantRepository := &participantRepository{updateErr: participants.ErrConflict}
	handler := newHandler(t, participantRepository, &turnRepository{}, "acct_01")
	path := "/api/v1/voice-sessions/vs_01/participants/p_01"

	request := accountRequest(http.MethodPatch, path, strings.NewReader(`{"provider_speaker_id":"diar_01"}`), "acct_01", true)
	response := serve(handler, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want %d; body = %s", response.Code, http.StatusConflict, response.Body.String())
	}
	var body recordsv1.ErrorResponse
	decodeBody(t, response, &body)
	if body.Error.Code != recordsv1.ErrorConflict {
		t.Fatalf("conflict error code = %q, want %q", body.Error.Code, recordsv1.ErrorConflict)
	}
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
	if !turnRepository.attributionUpdate.SpeakerConfidenceSet || turnRepository.attributionUpdate.SpeakerConfidence == nil || *turnRepository.attributionUpdate.SpeakerConfidence != 0.8 {
		t.Fatalf("attribution confidence = %#v", turnRepository.attributionUpdate)
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

func TestCorrectAttributionConfidencePresenceSemantics(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantSet bool
		want    *float64
	}{
		{name: "absent", body: `{"participant_id":"p_01","attribution_status":"confirmed"}`, wantSet: false},
		{name: "explicit null", body: `{"participant_id":"p_01","attribution_status":"confirmed","speaker_confidence":null}`, wantSet: true},
		{name: "number", body: `{"participant_id":"p_01","attribution_status":"confirmed","speaker_confidence":0.7}`, wantSet: true, want: ptr64(0.7)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turnRepository := &turnRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: true}
			handler := newHandler(t, &participantRepository{}, turnRepository, "acct_01")
			path := "/api/v1/voice-turns/vt_01/attribution"
			request := accountRequest(http.MethodPatch, path, strings.NewReader(test.body), "acct_01", true)
			response := serve(handler, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if turnRepository.attributionUpdate.SpeakerConfidenceSet != test.wantSet {
				t.Fatalf("SpeakerConfidenceSet = %v, want %v", turnRepository.attributionUpdate.SpeakerConfidenceSet, test.wantSet)
			}
			if !equalConfidence(turnRepository.attributionUpdate.SpeakerConfidence, test.want) {
				t.Fatalf("SpeakerConfidence = %v, want %v", turnRepository.attributionUpdate.SpeakerConfidence, test.want)
			}
		})
	}
}

func TestCorrectAttributionRejectsOutOfRangeConfidence(t *testing.T) {
	turnRepository := &turnRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: true}
	handler := newHandler(t, &participantRepository{}, turnRepository, "acct_01")
	path := "/api/v1/voice-turns/vt_01/attribution"
	body := `{"participant_id":"p_01","attribution_status":"confirmed","speaker_confidence":1.5}`
	request := accountRequest(http.MethodPatch, path, strings.NewReader(body), "acct_01", true)
	response := serve(handler, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range confidence status = %d, want 400; body = %s", response.Code, response.Body.String())
	}
}

func TestPATCHRejectsOversizedBody(t *testing.T) {
	validBody := `{"participant_id":"p_01","attribution_status":"confirmed"}`
	tests := []struct {
		name string
		body string
	}{
		{name: "truncated JSON value", body: `{"participant_id":"` + strings.Repeat("p", maxRequestBodyBytes) + `","attribution_status":"confirmed"}`},
		{name: "complete JSON followed by overflow", body: validBody + strings.Repeat(" ", maxRequestBodyBytes-len(validBody)+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			turnRepository := &turnRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: true}
			handler := newHandler(t, &participantRepository{}, turnRepository, "acct_01")
			request := accountRequest(http.MethodPatch, "/api/v1/voice-turns/vt_01/attribution", strings.NewReader(test.body), "acct_01", true)
			response := serve(handler, request)
			assertError(t, response, http.StatusBadRequest, recordsv1.ErrorInvalidRequest)
		})
	}
}

func TestDecodeJSONRejectsMalformedAndTrailingInput(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "syntax error", body: `{"display_name":`},
		{name: "two values", body: `{"display_name":"A"}{}`},
		{name: "trailing non whitespace", body: `{"display_name":"A"}x`},
		{name: "unknown field", body: `{"unknown":true}`},
		{name: "wrong field type", body: `{"display_name":123}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeParticipantUpdate(strings.NewReader(test.body)); err == nil {
				t.Fatal("decodeParticipantUpdate() error = nil, want error")
			}
		})
	}
}

func TestDecodeParticipantUpdatePreservesMissingAndNullFields(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantSet   bool
		wantValue *string
	}{
		{name: "missing", body: `{}`, wantSet: false},
		{name: "explicit null", body: `{"voice_profile_id":null}`, wantSet: true},
		{name: "value", body: `{"voice_profile_id":"vp_01"}`, wantSet: true, wantValue: stringPtr("vp_01")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			update, err := decodeParticipantUpdate(strings.NewReader(test.body))
			if err != nil {
				t.Fatalf("decodeParticipantUpdate() error = %v", err)
			}
			if update.VoiceProfileIDSet != test.wantSet || !equalString(update.VoiceProfileID, test.wantValue) {
				t.Fatalf("update = %#v, want set=%v value=%v", update, test.wantSet, test.wantValue)
			}
		})
	}
}

func TestPATCHInputErrorsDoNotCallRepositories(t *testing.T) {
	tests := []struct {
		name   string
		target string
		body   string
	}{
		{name: "participant empty", target: "/api/v1/voice-sessions/vs_01/participants/p_01", body: ""},
		{name: "participant malformed", target: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"display_name":`},
		{name: "participant unknown", target: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"unknown":true}`},
		{name: "participant wrong type", target: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"display_name":123}`},
		{name: "attribution empty", target: "/api/v1/voice-turns/vt_01/attribution", body: ""},
		{name: "attribution malformed", target: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":`},
		{name: "attribution two values", target: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":"p_01","attribution_status":"confirmed"}{}`},
		{name: "attribution unknown", target: "/api/v1/voice-turns/vt_01/attribution", body: `{"unknown":true}`},
		{name: "attribution wrong type", target: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":123,"attribution_status":"confirmed"}`},
		{name: "attribution confidence wrong type", target: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":"p_01","attribution_status":"confirmed","speaker_confidence":"high"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			participantRepository := &participantRepository{updated: recordsv1.Participant{ID: "p_01"}}
			turnRepository := &turnRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: true}
			handler := newHandler(t, participantRepository, turnRepository, "acct_01")
			response := serve(handler, accountRequest(http.MethodPatch, test.target, strings.NewReader(test.body), "acct_01", true))
			assertError(t, response, http.StatusBadRequest, recordsv1.ErrorInvalidRequest)
			if participantRepository.updateCalls != 0 || turnRepository.attributionCalls != 0 {
				t.Fatalf("invalid body called repository: participant=%d attribution=%d", participantRepository.updateCalls, turnRepository.attributionCalls)
			}
		})
	}
}

func TestInvalidRequestsReturnFieldDetails(t *testing.T) {
	handler := newHandler(t, &participantRepository{}, &turnRepository{
		turn:                 recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"},
		participantInSession: true,
	}, "acct_01")

	tests := []struct {
		name   string
		method string
		target string
		body   string
		system bool
		code   recordsv1.ErrorCode
		field  string
	}{
		{name: "invalid limit", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/participants?limit=101", code: recordsv1.ErrorInvalidRequest, field: "limit"},
		{name: "invalid attribution status", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/turns?attribution_status=invalid", code: recordsv1.ErrorInvalidRequest, field: "attribution_status"},
		{name: "invalid history time", method: http.MethodGet, target: "/api/v1/translation-history?created_from=not-a-time", code: recordsv1.ErrorInvalidRequest, field: "created_from"},
		{name: "reversed history range", method: http.MethodGet, target: "/api/v1/translation-history?created_from=2026-07-24T09:00:00Z&created_to=2026-07-24T08:00:00Z", code: recordsv1.ErrorInvalidRequest, field: "created_from"},
		{name: "empty participant update", method: http.MethodPatch, target: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{}`, system: true, code: recordsv1.ErrorInvalidRequest, field: "body"},
		{name: "unknown participant update field", method: http.MethodPatch, target: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"unknown":true}`, system: true, code: recordsv1.ErrorInvalidRequest, field: "body"},
		{name: "missing attribution participant", method: http.MethodPatch, target: "/api/v1/voice-turns/vt_01/attribution", body: `{"attribution_status":"confirmed"}`, system: true, code: recordsv1.ErrorInvalidAttribution, field: "participant_id"},
		{name: "invalid attribution status body", method: http.MethodPatch, target: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":"p_01","attribution_status":"pending"}`, system: true, code: recordsv1.ErrorInvalidAttribution, field: "attribution_status"},
		{name: "out of range confidence", method: http.MethodPatch, target: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":"p_01","attribution_status":"confirmed","speaker_confidence":1.5}`, system: true, code: recordsv1.ErrorInvalidAttribution, field: "speaker_confidence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body *strings.Reader
			if test.body != "" {
				body = strings.NewReader(test.body)
			}
			response := serve(handler, accountRequest(test.method, test.target, body, "acct_01", test.system))
			assertErrorField(t, response, test.code, test.field)
		})
	}
}

func TestQueryValidationRejectsInvalidParametersBeforeRepository(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		field  string
		route  string
	}{
		{name: "participants unknown", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/participants?unexpected=1", field: "unexpected", route: "participants"},
		{name: "participants duplicate", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/participants?limit=2&limit=3", field: "limit", route: "participants"},
		{name: "participants zero", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/participants?limit=0", field: "limit", route: "participants"},
		{name: "participants negative", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/participants?limit=-1", field: "limit", route: "participants"},
		{name: "participants non numeric", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/participants?limit=nope", field: "limit", route: "participants"},
		{name: "participants over maximum", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/participants?limit=101", field: "limit", route: "participants"},
		{name: "session turns unknown", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/turns?unexpected=1", field: "unexpected", route: "session turns"},
		{name: "session turns duplicate", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/turns?limit=2&limit=3", field: "limit", route: "session turns"},
		{name: "session turns zero", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/turns?limit=0", field: "limit", route: "session turns"},
		{name: "session turns negative", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/turns?limit=-1", field: "limit", route: "session turns"},
		{name: "session turns non numeric", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/turns?limit=nope", field: "limit", route: "session turns"},
		{name: "session turns over maximum", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/turns?limit=101", field: "limit", route: "session turns"},
		{name: "session turns invalid attribution", method: http.MethodGet, target: "/api/v1/voice-sessions/vs_01/turns?attribution_status=unknown", field: "attribution_status", route: "session turns"},
		{name: "history unknown", method: http.MethodGet, target: "/api/v1/translation-history?unexpected=1", field: "unexpected", route: "history"},
		{name: "history duplicate", method: http.MethodGet, target: "/api/v1/translation-history?limit=2&limit=3", field: "limit", route: "history"},
		{name: "history zero", method: http.MethodGet, target: "/api/v1/translation-history?limit=0", field: "limit", route: "history"},
		{name: "history negative", method: http.MethodGet, target: "/api/v1/translation-history?limit=-1", field: "limit", route: "history"},
		{name: "history non numeric", method: http.MethodGet, target: "/api/v1/translation-history?limit=nope", field: "limit", route: "history"},
		{name: "history over maximum", method: http.MethodGet, target: "/api/v1/translation-history?limit=101", field: "limit", route: "history"},
		{name: "history invalid from", method: http.MethodGet, target: "/api/v1/translation-history?created_from=not-a-time", field: "created_from", route: "history"},
		{name: "history invalid to", method: http.MethodGet, target: "/api/v1/translation-history?created_to=not-a-time", field: "created_to", route: "history"},
		{name: "history reversed range", method: http.MethodGet, target: "/api/v1/translation-history?created_from=2026-07-24T09:00:00Z&created_to=2026-07-24T08:00:00Z", field: "created_from", route: "history"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			participantsRepository := &participantRepository{}
			turnRepository := &turnRepository{}
			handler := newHandler(t, participantsRepository, turnRepository, "acct_01")
			response := serve(handler, accountRequest(test.method, test.target, nil, "acct_01", false))
			assertErrorField(t, response, recordsv1.ErrorInvalidRequest, test.field)
			if participantsRepository.listCalls != 0 || turnRepository.listSessionCalls != 0 || turnRepository.historyCalls != 0 {
				t.Fatalf("invalid %s query called repository: participants=%d session turns=%d history=%d", test.route, participantsRepository.listCalls, turnRepository.listSessionCalls, turnRepository.historyCalls)
			}
		})
	}
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

func TestWriteDomainErrorMapsContractAndHidesUnknownCause(t *testing.T) {
	secret := errors.New("dial postgres password=secret")
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   recordsv1.ErrorCode
		wantField  string
		wantMsg    string
	}{
		{name: "not implemented", err: errNotImplemented, wantStatus: http.StatusNotImplemented, wantCode: recordsv1.ErrorNotImplemented, wantMsg: errNotImplemented.Error()},
		{name: "participant session absent", err: participants.ErrSessionNotFound, wantStatus: http.StatusNotFound, wantCode: recordsv1.ErrorVoiceSessionAbsent, wantMsg: participants.ErrSessionNotFound.Error()},
		{name: "turn session absent", err: turns.ErrSessionNotFound, wantStatus: http.StatusNotFound, wantCode: recordsv1.ErrorVoiceSessionAbsent, wantMsg: turns.ErrSessionNotFound.Error()},
		{name: "participant absent", err: participants.ErrParticipantNotFound, wantStatus: http.StatusNotFound, wantCode: recordsv1.ErrorParticipantAbsent, wantMsg: participants.ErrParticipantNotFound.Error()},
		{name: "turn participant absent", err: turns.ErrParticipantNotFound, wantStatus: http.StatusNotFound, wantCode: recordsv1.ErrorParticipantAbsent, wantMsg: turns.ErrParticipantNotFound.Error()},
		{name: "turn absent", err: turns.ErrTurnNotFound, wantStatus: http.StatusNotFound, wantCode: recordsv1.ErrorVoiceTurnAbsent, wantMsg: turns.ErrTurnNotFound.Error()},
		{name: "participant forbidden", err: participants.ErrForbidden, wantStatus: http.StatusForbidden, wantCode: recordsv1.ErrorForbidden, wantMsg: participants.ErrForbidden.Error()},
		{name: "turn forbidden", err: turns.ErrForbidden, wantStatus: http.StatusForbidden, wantCode: recordsv1.ErrorForbidden, wantMsg: turns.ErrForbidden.Error()},
		{name: "conflict", err: participants.ErrConflict, wantStatus: http.StatusConflict, wantCode: recordsv1.ErrorConflict, wantMsg: participants.ErrConflict.Error()},
		{name: "invalid attribution", err: domain.NewFieldError("participant_id", turns.ErrInvalidAttribution), wantStatus: http.StatusBadRequest, wantCode: recordsv1.ErrorInvalidAttribution, wantField: "participant_id", wantMsg: "participant_id: invalid voice turn attribution"},
		{name: "invalid request", err: domain.NewFieldError("limit", participants.ErrInvalidRequest), wantStatus: http.StatusBadRequest, wantCode: recordsv1.ErrorInvalidRequest, wantField: "limit", wantMsg: "limit: invalid participant request"},
		{name: "unknown internal", err: secret, wantStatus: http.StatusInternalServerError, wantCode: recordsv1.ErrorInternal, wantMsg: "internal server error"},
	}
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/voice-turns/vt_01", nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.writeDomainError(response, request, test.err)
			var body recordsv1.ErrorResponse
			decodeBody(t, response, &body)
			if response.Code != test.wantStatus || body.Error.Code != test.wantCode {
				t.Fatalf("response = status %d body %#v, want status %d code %q", response.Code, body, test.wantStatus, test.wantCode)
			}
			if body.Error.RequestID == "" {
				t.Fatal("request ID is empty")
			}
			if body.Error.Message != test.wantMsg {
				t.Fatalf("message = %q, want %q", body.Error.Message, test.wantMsg)
			}
			if test.wantField == "" {
				if body.Error.Details != nil {
					t.Fatalf("details = %#v, want nil", body.Error.Details)
				}
			} else if body.Error.Details == nil || body.Error.Details.Field != test.wantField {
				t.Fatalf("details = %#v, want field %q", body.Error.Details, test.wantField)
			}
			if test.err == secret && strings.Contains(response.Body.String(), secret.Error()) {
				t.Fatalf("response exposed internal cause: %s", response.Body.String())
			}
		})
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

func TestRegisterAppliesAuthenticationToEveryRoute(t *testing.T) {
	participantRepository := &participantRepository{}
	turnRepository := &turnRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: true}
	owners := sessionOwners{ownerID: "acct_01"}
	server := NewHandler(Dependencies{
		Participants: participants.NewService(participantRepository, owners, nil),
		Turns:        turns.NewService(turnRepository, owners, nil),
		Accounts:     ContextAccountProvider{},
		System:       ContextSystemAuthorizer{},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	accountCalls := 0
	accountAuthenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			accountCalls++
			if request.Header.Get("Authorization") != "Bearer access-token" {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			ctx := WithAccountID(request.Context(), "acct_01")
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	}
	systemCalls := 0
	systemAuthenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			systemCalls++
			if request.Header.Get(systemTokenHeader) != "system-token" {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			ctx := WithSystemActor(request.Context())
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	}
	mux := http.NewServeMux()
	server.Register(mux, RouteMiddleware{
		Account: accountAuthenticate,
		System:  systemAuthenticate,
	})

	noAuthorization := httptest.NewRequest(http.MethodGet, "/api/v1/translation-history", nil)
	if response := serve(mux, noAuthorization); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	accountOnly := []struct {
		method     string
		path       string
		wantStatus int
	}{
		{method: http.MethodGet, path: "/api/v1/voice-sessions/vs_01/participants", wantStatus: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/voice-sessions/vs_01/turns", wantStatus: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/voice-turns/vt_01", wantStatus: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/translation-history", wantStatus: http.StatusOK},
	}
	for _, test := range accountOnly {
		request := httptest.NewRequest(test.method, test.path, nil)
		request.Header.Set("Authorization", "Bearer access-token")
		response := serve(mux, request)
		if response.Code != test.wantStatus {
			t.Fatalf("account-only %s %s status = %d, want %d; body = %s", test.method, test.path, response.Code, test.wantStatus, response.Body.String())
		}
	}

	missingSystem := []struct {
		method     string
		path       string
		wantStatus int
	}{
		{method: http.MethodPatch, path: "/api/v1/voice-sessions/vs_01/participants/p_01", wantStatus: http.StatusForbidden},
		{method: http.MethodPatch, path: "/api/v1/voice-turns/vt_01/attribution", wantStatus: http.StatusForbidden},
	}
	for _, test := range missingSystem {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{"participant_id":"p_01","attribution_status":"confirmed"}`))
		request.Header.Set("Authorization", "Bearer access-token")
		response := serve(mux, request)
		if response.Code != test.wantStatus {
			t.Fatalf("missing system %s %s status = %d, want %d; body = %s", test.method, test.path, response.Code, test.wantStatus, response.Body.String())
		}
	}

	withSystem := []struct {
		method     string
		path       string
		body       string
		wantStatus int
	}{
		{method: http.MethodPatch, path: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"display_name":"A"}`, wantStatus: http.StatusOK},
		{method: http.MethodPatch, path: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":"p_01","attribution_status":"confirmed"}`, wantStatus: http.StatusOK},
	}
	for _, test := range withSystem {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer access-token")
		request.Header.Set(systemTokenHeader, "system-token")
		response := serve(mux, request)
		if response.Code != test.wantStatus {
			t.Fatalf("with system %s %s status = %d, want %d; body = %s", test.method, test.path, response.Code, test.wantStatus, response.Body.String())
		}
	}
}

func TestSystemAuthenticateRejectsWithoutConfiguredToken(t *testing.T) {
	participantRepository := &participantRepository{}
	turnRepository := &turnRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: true}
	owners := sessionOwners{ownerID: "acct_01"}
	server := NewHandler(Dependencies{
		Participants: participants.NewService(participantRepository, owners, nil),
		Turns:        turns.NewService(turnRepository, owners, nil),
		Accounts:     ContextAccountProvider{},
		System:       ContextSystemAuthorizer{},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := http.NewServeMux()
	server.Register(mux, RouteMiddleware{
		Account: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				next.ServeHTTP(writer, request.WithContext(WithAccountID(request.Context(), "acct_01")))
			})
		},
		System: server.SystemAuthenticate,
	})

	path := "/api/v1/voice-sessions/vs_01/participants/p_01"
	request := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"display_name":"A"}`))
	request.Header.Set(systemTokenHeader, "any-token")
	response := serve(mux, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("system authenticate status = %d, want %d; body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}
}

func TestSystemAuthenticateAcceptsConfiguredToken(t *testing.T) {
	participantRepository := &participantRepository{updated: recordsv1.Participant{ID: "p_01", SessionID: "vs_01"}}
	turnRepository := &turnRepository{turn: recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, participantInSession: true}
	owners := sessionOwners{ownerID: "acct_01"}
	server := NewHandler(Dependencies{
		Participants: participants.NewService(participantRepository, owners, nil),
		Turns:        turns.NewService(turnRepository, owners, nil),
		Accounts:     ContextAccountProvider{},
		System:       ContextSystemAuthorizer{},
		SystemToken:  "records-system-token-secret-123456",
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := http.NewServeMux()
	server.Register(mux, RouteMiddleware{
		Account: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				next.ServeHTTP(writer, request.WithContext(WithAccountID(request.Context(), "acct_01")))
			})
		},
		System: server.SystemAuthenticate,
	})

	tests := []struct {
		name  string
		token string
		path  string
		body  string
		want  int
	}{
		{name: "matching token", token: "records-system-token-secret-123456", path: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"display_name":"A"}`, want: http.StatusOK},
		{name: "wrong token", token: "wrong-token", path: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"display_name":"A"}`, want: http.StatusForbidden},
		{name: "missing header", path: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"display_name":"A"}`, want: http.StatusForbidden},
		{name: "attribution matching token", token: "records-system-token-secret-123456", path: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":"p_01","attribution_status":"confirmed"}`, want: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPatch, test.path, strings.NewReader(test.body))
			if test.token != "" {
				request.Header.Set(systemTokenHeader, test.token)
			}
			response := serve(mux, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.want, response.Body.String())
			}
		})
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
	handler.Register(mux, RouteMiddleware{
		Account: func(next http.Handler) http.Handler { return next },
		System:  func(next http.Handler) http.Handler { return next },
	})
	return mux
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	return NewHandler(validDependencies())
}

func validDependencies() Dependencies {
	owners := sessionOwners{ownerID: "acct_01"}
	return Dependencies{
		Participants: participants.NewService(&participantRepository{}, owners, nil),
		Turns:        turns.NewService(&turnRepository{}, owners, nil),
		Accounts:     ContextAccountProvider{},
		System:       ContextSystemAuthorizer{},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func assertPanics(t *testing.T, function func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("function did not panic")
		}
	}()
	function()
}

type accessTokenVerifier struct {
	claims accounts.AccessTokenClaims
	err    error
	calls  int
}

func (v *accessTokenVerifier) VerifyAccessToken(context.Context, string) (accounts.AccessTokenClaims, error) {
	v.calls++
	return v.claims, v.err
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
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

func assertErrorField(t *testing.T, response *httptest.ResponseRecorder, wantCode recordsv1.ErrorCode, wantField string) {
	t.Helper()
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var body recordsv1.ErrorResponse
	decodeBody(t, response, &body)
	if body.Error.Code != wantCode || body.Error.Details == nil || body.Error.Details.Field != wantField {
		t.Fatalf("error body = %#v, want code %q and field %q", body, wantCode, wantField)
	}
}

func decodeBody(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func ptr64(value float64) *float64 {
	return &value
}

func stringPtr(value string) *string {
	return &value
}

func equalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func equalConfidence(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

type participantRepository struct {
	listResponse recordsv1.ParticipantListResponse
	listQuery    recordsv1.ListParticipantsQuery
	listCalls    int
	updated      recordsv1.Participant
	update       participants.Update
	updateCalls  int
	updateErr    error
}

func (r *participantRepository) List(_ context.Context, _, _ string, query recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error) {
	r.listCalls++
	r.listQuery = query
	return r.listResponse, nil
}

func (r *participantRepository) Update(_ context.Context, _ string, _ string, update participants.Update) (recordsv1.Participant, error) {
	r.updateCalls++
	r.update = update
	if r.updateErr != nil {
		return recordsv1.Participant{}, r.updateErr
	}
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
	listSessionCalls     int
	historyResponse      recordsv1.VoiceTurnListResponse
	historyAccountID     string
	historyQuery         recordsv1.ListTurnsQuery
	historyCalls         int
	participantInSession bool
	attributionUpdate    turns.AttributionUpdate
	attributionCalls     int
}

func (*turnRepository) StoreFinalTurn(context.Context, recordsv1.FinalTurnEvent) error {
	return nil
}

func (r *turnRepository) ListSession(_ context.Context, _, _ string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	r.listSessionCalls++
	r.listQuery = query
	return r.listResponse, nil
}

func (r *turnRepository) Find(context.Context, string, string) (recordsv1.VoiceTurn, error) {
	if r.findErr != nil {
		return recordsv1.VoiceTurn{}, r.findErr
	}
	return r.turn, nil
}

func (r *turnRepository) ListHistory(_ context.Context, accountID string, query recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	r.historyCalls++
	r.historyAccountID = accountID
	r.historyQuery = query
	return r.historyResponse, nil
}

func (r *turnRepository) CorrectAttribution(_ context.Context, update turns.AttributionUpdate) (recordsv1.VoiceTurn, error) {
	r.attributionCalls++
	if !r.participantInSession {
		return recordsv1.VoiceTurn{}, turns.ErrInvalidAttribution
	}
	r.attributionUpdate = update
	updated := r.turn
	updated.ParticipantID = &update.ParticipantID
	updated.AttributionStatus = update.AttributionStatus
	if update.SpeakerConfidenceSet {
		updated.SpeakerConfidence = update.SpeakerConfidence
	}
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
