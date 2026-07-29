//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
)

func TestMember5ProtectedRoutesRequireBearerToken(t *testing.T) {
	databaseURL := recordsHTTPTestDatabaseURL(t)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("JWT_SECRET", strings.Repeat("j", 36))
	t.Setenv("AUTH_PEPPER", strings.Repeat("p", 36))
	t.Setenv("DESTINATION_ENCRYPTION_KEY", integrationDestinationKey(t))
	t.Setenv("VERIFICATION_SENDER", "log")

	dependencies, err := newRecordsHTTPDependencies(t.Context())
	if err != nil {
		t.Fatalf("newRecordsHTTPDependencies() error = %v", err)
	}
	t.Cleanup(dependencies.cleanup)

	handler := buildMux(
		languages.NewHandler(nil, nil),
		dependencies.handler,
		dependencies.accounts,
		dependencies.usage,
		dependencies.delivery,
		dependencies.tokens,
		dependencies.pool,
		dependencies.redisPing,
	)

	protected := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/account/me"},
		{http.MethodGet, "/api/v1/voice-sessions/session_test/usage"},
		{http.MethodGet, "/api/v1/usage/summary?period_start=2026-07-01T00:00:00Z&period_end=2026-08-01T00:00:00Z"},
		{http.MethodPost, "/api/v1/outbound-messages"},
		{http.MethodGet, "/api/v1/account/message-preferences"},
	}
	for _, route := range protected {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status without token = %d, want 401, body = %s", response.Code, response.Body.String())
			}

			request = httptest.NewRequest(route.method, route.path, nil)
			request.Header.Set("Authorization", "Bearer invalid-token")
			request.Header.Set("X-Account-ID", "forged-account")
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status with forged headers = %d, want 401, body = %s", response.Code, response.Body.String())
			}
		})
	}

	public := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/v1/auth/anonymous", ""},
		{http.MethodPost, "/api/v1/auth/verification-codes", `{"phone":"+8613800138000"}`},
	}
	for _, route := range public {
		t.Run("public "+route.method+" "+route.path, func(t *testing.T) {
			var request *http.Request
			if route.body != "" {
				request = httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
				request.Header.Set("Content-Type", "application/json")
			} else {
				request = httptest.NewRequest(route.method, route.path, nil)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code == http.StatusUnauthorized {
				t.Fatalf("public route returned 401, body = %s", response.Body.String())
			}
		})
	}
}
