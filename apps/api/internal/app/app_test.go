package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/config"
)

func TestNewWiresMockIdentityAccessSession(t *testing.T) {
	router := New(config.Config{Mode: "test", IdentityProvider: config.IdentityProviderMock})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/identity/access-sessions",
		strings.NewReader(`{"provider_code":"demo","fixture_code":"staff-active"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	var body struct {
		Data struct {
			AccessSessionID string `json:"access_session_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.AccessSessionID != "session-demo-staff-001" {
		t.Fatalf("AccessSessionID = %q", body.Data.AccessSessionID)
	}
}

func TestNewLeavesIdentityRouteDisabledByDefault(t *testing.T) {
	router := New(config.Config{Mode: "test"})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/identity/access-sessions",
		strings.NewReader(`{"provider_code":"demo","fixture_code":"staff-active"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}
