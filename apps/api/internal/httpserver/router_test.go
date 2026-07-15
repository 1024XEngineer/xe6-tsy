package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/config"
	"github.com/1024XEngineer/xe6-tsy/apps/api/internal/modules"
)

func TestRouterStartsWithoutFeatureRoutes(t *testing.T) {
	router := New(config.Config{Mode: "test"}, modules.Foundation())

	health := httptest.NewRecorder()
	router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}

	feature := httptest.NewRecorder()
	router.ServeHTTP(feature, httptest.NewRequest(http.MethodGet, "/api/v1/identity", nil))
	if feature.Code != http.StatusNotFound {
		t.Fatalf("unimplemented feature status = %d", feature.Code)
	}
}
