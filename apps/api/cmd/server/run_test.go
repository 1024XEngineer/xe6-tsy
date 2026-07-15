package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestNewRouter_Health(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := newRouter(nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Body.String(); got != "{\"status\":\"ok\"}" {
		t.Fatalf("body = %q, want health response", got)
	}
}

func TestServerAddress(t *testing.T) {
	tests := []struct {
		name string
		port string
		want string
	}{
		{name: "default port", port: "", want: ":8080"},
		{name: "configured port", port: "9090", want: ":9090"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := serverAddress(tt.port); got != tt.want {
				t.Fatalf("serverAddress(%q) = %q, want %q", tt.port, got, tt.want)
			}
		})
	}
}

func TestRunRejectsInvalidAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := run("127.0.0.1:-1"); err == nil {
		t.Fatal("run() error = nil, want invalid address error")
	}
}
