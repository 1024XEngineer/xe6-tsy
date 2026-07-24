package languages

import "net/http"

// Handler serves the language-configuration HTTP surface from issue #88 §3.
// All routes currently return 501 not_implemented (empty frontend contract).
type Handler struct{}

// NewHandler returns HTTP handlers for the language-configuration routes.
func NewHandler() *Handler {
	return &Handler{}
}

// Register attaches language routes onto mux.
// Paths match issue #88; Go 1.22+ method-aware patterns are required.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/languages", h.listLanguages)
	mux.HandleFunc("GET /api/v1/voice-sessions/{id}/language-config", h.getCurrentConfig)
	mux.HandleFunc("POST /api/v1/voice-sessions/{id}/language-configs", h.createConfig)
	mux.HandleFunc("GET /api/v1/voice-sessions/{id}/language-configs", h.listConfigHistory)
}

func (h *Handler) listLanguages(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, r)
}

func (h *Handler) getCurrentConfig(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, r)
}

func (h *Handler) createConfig(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, r)
}

func (h *Handler) listConfigHistory(w http.ResponseWriter, r *http.Request) {
	notImplemented(w, r)
}
