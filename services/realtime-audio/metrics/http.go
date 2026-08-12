package metrics

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// Register mounts a read-only JSON snapshot on GET /metrics. The endpoint has
// no per-session values and is intended for an internal monitoring listener or
// an ingress rule that keeps it outside the public realtime API.
func Register(mux *http.ServeMux, registry *Registry, token string) {
	token = strings.TrimSpace(token)
	if mux == nil || registry == nil || token == "" {
		return
	}
	mux.HandleFunc("GET /metrics", func(writer http.ResponseWriter, request *http.Request) {
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(registry.Current())
	})
}
