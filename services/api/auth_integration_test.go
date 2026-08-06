//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
)

func TestAuthHTTPRefreshRotationAndPersistence(t *testing.T) {
	databaseURL := recordsHTTPTestDatabaseURL(t)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 36))
	t.Setenv("AUTH_PEPPER", strings.Repeat("p", 36))
	t.Setenv("VERIFICATION_SENDER", "log")

	dependencies, err := newRecordsHTTPDependencies(t.Context())
	if err != nil {
		t.Fatalf("newRecordsHTTPDependencies() error = %v", err)
	}
	t.Cleanup(dependencies.cleanup)

	handler := buildMux(
		languages.NewHandler(nil, nil),
		nil,
		dependencies.handler,
		dependencies.accounts,
		dependencies.tokens,
	)

	created := postAuthJSON(t, handler, http.MethodPost, "/api/v1/auth/anonymous", nil, http.StatusCreated)
	var anonymous accounts.AuthResult
	decodeAuthBody(t, created, &anonymous)

	refreshed := postAuthJSON(t, handler, http.MethodPost, "/api/v1/auth/token/refresh", map[string]string{
		"refresh_token": anonymous.Tokens.RefreshToken,
	}, http.StatusOK)
	var rotated accounts.Tokens
	decodeAuthBody(t, refreshed, &rotated)
	if rotated.RefreshToken == "" || rotated.RefreshToken == anonymous.Tokens.RefreshToken {
		t.Fatalf("rotated refresh token = %q, want a new non-empty token", rotated.RefreshToken)
	}

	replay := postAuthJSON(t, handler, http.MethodPost, "/api/v1/auth/token/refresh", map[string]string{
		"refresh_token": anonymous.Tokens.RefreshToken,
	}, http.StatusUnauthorized)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("replay refresh status = %d, want %d", replay.Code, http.StatusUnauthorized)
	}

	restarted, err := newRecordsHTTPDependencies(t.Context())
	if err != nil {
		t.Fatalf("restart dependencies: %v", err)
	}
	t.Cleanup(restarted.cleanup)
	restartedHandler := buildMux(
		languages.NewHandler(nil, nil),
		nil,
		restarted.handler,
		restarted.accounts,
		restarted.tokens,
	)

	meRequest := httptest.NewRequest(http.MethodGet, "/api/v1/account/me", nil)
	meRequest.Header.Set("Authorization", "Bearer "+rotated.AccessToken)
	meResponse := httptest.NewRecorder()
	restartedHandler.ServeHTTP(meResponse, meRequest)
	if meResponse.Code != http.StatusOK {
		t.Fatalf("restarted me status = %d, want %d, body = %s", meResponse.Code, http.StatusOK, meResponse.Body.String())
	}
	var account accounts.Account
	decodeAuthBody(t, meResponse, &account)
	if account.ID != anonymous.Account.ID {
		t.Fatalf("restarted account = %q, want %q", account.ID, anonymous.Account.ID)
	}
}

func TestAuthHTTPPhoneVerificationSingleUse(t *testing.T) {
	databaseURL := recordsHTTPTestDatabaseURL(t)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 36))
	t.Setenv("AUTH_PEPPER", strings.Repeat("p", 36))
	t.Setenv("VERIFICATION_SENDER", "log")

	dependencies, err := newRecordsHTTPDependencies(t.Context())
	if err != nil {
		t.Fatalf("buildRecordsHTTPDependencies() error = %v", err)
	}
	t.Cleanup(dependencies.cleanup)

	handler := buildMux(
		languages.NewHandler(nil, nil),
		nil,
		dependencies.handler,
		dependencies.accounts,
		dependencies.tokens,
	)

	const phone = "+8613800138000"
	challengeResponse := postAuthJSON(t, handler, http.MethodPost, "/api/v1/auth/verification-codes", map[string]string{
		"phone": phone,
	}, http.StatusAccepted)
	var challenge struct {
		ChallengeID string `json:"challenge_id"`
	}
	decodeAuthBody(t, challengeResponse, &challenge)

	loginResponse := postAuthJSON(t, handler, http.MethodPost, "/api/v1/auth/phone/login", map[string]string{
		"challenge_id": challenge.ChallengeID,
		"code":         "8888",
	}, http.StatusOK)
	var login accounts.AuthResult
	decodeAuthBody(t, loginResponse, &login)
	if login.Account.Kind != accounts.AccountKindRegistered {
		t.Fatalf("login account kind = %q, want registered", login.Account.Kind)
	}

	replayResponse := postAuthJSON(t, handler, http.MethodPost, "/api/v1/auth/phone/login", map[string]string{
		"challenge_id": challenge.ChallengeID,
		"code":         "8888",
	}, http.StatusUnauthorized)
	if replayResponse.Code != http.StatusUnauthorized {
		t.Fatalf("replay login status = %d, want %d", replayResponse.Code, http.StatusUnauthorized)
	}
}

func postAuthJSON(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()
	var payload *bytes.Buffer
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		payload = bytes.NewBuffer(encoded)
	} else {
		payload = bytes.NewBuffer(nil)
	}
	request := httptest.NewRequest(method, path, payload)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, path, response.Code, wantStatus, response.Body.String())
	}
	return response
}

func decodeAuthBody(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response body: %v, body = %s", err, response.Body.String())
	}
}
