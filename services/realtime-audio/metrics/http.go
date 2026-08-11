package metrics

import (
	"encoding/json"
	"net/http"
)

// Register mounts a read-only JSON snapshot on GET /metrics. The endpoint has
// no per-session values and is intended for an internal monitoring listener or
// an ingress rule that keeps it outside the public realtime API.
func Register(mux *http.ServeMux, registry *Registry) {
	if mux == nil || registry == nil {
		return
	}
	mux.HandleFunc("GET /metrics", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(registry.Current())
	})
}
