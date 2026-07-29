package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
	recordswebapi "github.com/1024XEngineer/xe6-tsy/services/api/webapi"
)

func TestBuildMuxActivatesAuthenticatedVoiceRecordRoutes(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		path          string
		body          string
		accessToken   string
		wantStatus    int
		wantErrorCode recordsv1.ErrorCode
	}{
		{name: "missing token", method: http.MethodGet, path: "/api/v1/translation-history", wantStatus: http.StatusUnauthorized, wantErrorCode: recordsv1.ErrorUnauthenticated},
		{name: "invalid token", method: http.MethodGet, path: "/api/v1/translation-history", accessToken: "invalid", wantStatus: http.StatusUnauthorized, wantErrorCode: recordsv1.ErrorUnauthenticated},
		{name: "list participants", method: http.MethodGet, path: "/api/v1/voice-sessions/vs_01/participants", accessToken: "account-token", wantStatus: http.StatusOK},
		{name: "update participant stays system only", method: http.MethodPatch, path: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"voice_profile_id":null}`, accessToken: "account-token", wantStatus: http.StatusForbidden, wantErrorCode: recordsv1.ErrorForbidden},
		{name: "list session turns", method: http.MethodGet, path: "/api/v1/voice-sessions/vs_01/turns", accessToken: "account-token", wantStatus: http.StatusOK},
		{name: "get turn", method: http.MethodGet, path: "/api/v1/voice-turns/vt_01", accessToken: "account-token", wantStatus: http.StatusOK},
		{name: "correct attribution stays system only", method: http.MethodPatch, path: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":"p_01","attribution_status":"corrected"}`, accessToken: "account-token", wantStatus: http.StatusForbidden, wantErrorCode: recordsv1.ErrorForbidden},
		{name: "list history", method: http.MethodGet, path: "/api/v1/translation-history", accessToken: "account-token", wantStatus: http.StatusOK},
		{name: "cross account", method: http.MethodGet, path: "/api/v1/voice-sessions/vs_01/participants", accessToken: "other-account-token", wantStatus: http.StatusForbidden, wantErrorCode: recordsv1.ErrorForbidden},
		{name: "missing session", method: http.MethodGet, path: "/api/v1/voice-sessions/vs_missing/participants", accessToken: "account-token", wantStatus: http.StatusNotFound, wantErrorCode: recordsv1.ErrorVoiceSessionAbsent},
	}

	handler := buildMux(
		languages.NewHandler(nil, nil),
		newRecordsTestHandler(),
		accounts.NewUseCases(),
		mainTokenVerifier{},
	)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.accessToken != "" {
				request.Header.Set("Authorization", "Bearer "+test.accessToken)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantErrorCode != "" {
				var errorResponse recordsv1.ErrorResponse
				if err := json.Unmarshal(response.Body.Bytes(), &errorResponse); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if errorResponse.Error.Code != test.wantErrorCode {
					t.Fatalf("error code = %q, want %q", errorResponse.Error.Code, test.wantErrorCode)
				}
			}
		})
	}
}

func TestBuildMuxRegistersLanguageRoutes(t *testing.T) {
	handler := buildMux(
		languages.NewHandler(nil, func(*http.Request) (string, bool) {
			return "acct_01", true
		}),
		newRecordsTestHandler(),
		accounts.NewUseCases(),
		mainTokenVerifier{},
	)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/languages", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	// Nil service keeps language routes registered but explicitly unimplemented.
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, http.StatusNotImplemented, response.Body.String())
	}
}

func TestNewRecordsHTTPDependenciesRequiresConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		databaseURL string
		tokenSecret string
		wantError   string
	}{
		{name: "database URL", tokenSecret: strings.Repeat("s", 32), wantError: "DATABASE_URL is required"},
		{name: "token secret", databaseURL: "postgres://unused", wantError: "JWT_SECRET is required"},
		{name: "short token secret", databaseURL: "postgres://unused", tokenSecret: strings.Repeat("s", 31), wantError: "JWT_SECRET must be at least 32 bytes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", test.databaseURL)
			t.Setenv("JWT_SECRET", test.tokenSecret)

			_, err := newRecordsHTTPDependencies(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("newRecordsHTTPDependencies() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func newRecordsTestHandler() *recordswebapi.Server {
	owners := mainSessionOwners{}
	return recordswebapi.NewHandler(recordswebapi.Dependencies{
		Participants: participants.NewService(mainParticipantRepository{}, owners, nil),
		Turns:        turns.NewService(mainTurnRepository{}, owners, nil),
		Accounts:     recordswebapi.ContextAccountProvider{},
		System:       recordswebapi.ContextSystemAuthorizer{},
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

type mainTokenVerifier struct{}

func (mainTokenVerifier) VerifyAccessToken(_ context.Context, token string) (accounts.AccessTokenClaims, error) {
	switch token {
	case "account-token":
		return accounts.AccessTokenClaims{AccountID: "acct_01", SessionID: "auths_01"}, nil
	case "other-account-token":
		return accounts.AccessTokenClaims{AccountID: "acct_02", SessionID: "auths_02"}, nil
	default:
		return accounts.AccessTokenClaims{}, domain.ErrUnauthorized
	}
}

type mainSessionOwners struct{}

func (mainSessionOwners) AccountIDForSession(_ context.Context, sessionID string) (string, error) {
	if sessionID == "vs_missing" {
		return "", domain.ErrNotFound
	}
	return "acct_01", nil
}

type mainParticipantRepository struct{}

func (mainParticipantRepository) List(context.Context, string, string, recordsv1.ListParticipantsQuery) (recordsv1.ParticipantListResponse, error) {
	return recordsv1.ParticipantListResponse{}, nil
}

func (mainParticipantRepository) Update(context.Context, string, string, participants.Update) (recordsv1.Participant, error) {
	return recordsv1.Participant{}, nil
}

func (mainParticipantRepository) FindOrCreate(context.Context, recordsv1.SpeakerObservation) (recordsv1.Participant, error) {
	return recordsv1.Participant{}, nil
}

type mainTurnRepository struct{}

func (mainTurnRepository) StoreFinalTurn(context.Context, recordsv1.FinalTurnEvent) error {
	return nil
}

func (mainTurnRepository) ListSession(context.Context, string, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	return recordsv1.VoiceTurnListResponse{}, nil
}

func (mainTurnRepository) Find(context.Context, string, string) (recordsv1.VoiceTurn, error) {
	return recordsv1.VoiceTurn{ID: "vt_01", SessionID: "vs_01"}, nil
}

func (mainTurnRepository) ListHistory(context.Context, string, recordsv1.ListTurnsQuery) (recordsv1.VoiceTurnListResponse, error) {
	return recordsv1.VoiceTurnListResponse{}, nil
}

func (mainTurnRepository) CorrectAttribution(context.Context, turns.AttributionUpdate) (recordsv1.VoiceTurn, error) {
	return recordsv1.VoiceTurn{}, nil
}
