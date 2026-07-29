package languages

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

// AccountIDFromContext extracts the authenticated account ID set by trusted middleware.
type AccountIDFromContext func(r *http.Request) (accountID string, ok bool)

// Handler serves the language-configuration HTTP surface from issue #88 §3.
type Handler struct {
	svc       *Service
	accountID AccountIDFromContext
}

// NewHandler returns HTTP handlers backed by Service.
// accountID must read only middleware-injected identity (never client body fields).
func NewHandler(svc *Service, accountID AccountIDFromContext) *Handler {
	if accountID == nil {
		accountID = func(*http.Request) (string, bool) { return "", false }
	}
	return &Handler{svc: svc, accountID: accountID}
}

// Register attaches language routes behind the caller's authentication boundary.
func (h *Handler) Register(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	if authenticate == nil {
		panic("language authentication middleware is required")
	}
	mux.Handle("GET /api/v1/languages", authenticate(http.HandlerFunc(h.listLanguages)))
	mux.Handle("GET /api/v1/voice-sessions/{id}/language-config", authenticate(http.HandlerFunc(h.getCurrentConfig)))
	mux.Handle("POST /api/v1/voice-sessions/{id}/language-configs", authenticate(http.HandlerFunc(h.createConfig)))
	mux.Handle("GET /api/v1/voice-sessions/{id}/language-configs", authenticate(http.HandlerFunc(h.listConfigHistory)))
}

func (h *Handler) listLanguages(w http.ResponseWriter, r *http.Request) {
	if _, err := h.requireAccount(r); err != nil {
		writeServiceError(w, r, err)
		return
	}
	if h.svc == nil {
		writeServiceError(w, r, ErrNotImplemented)
		return
	}

	activeOnly := r.URL.Query().Get("active") == "true"
	langs, err := h.svc.ListSupportedLanguages(r.Context(), activeOnly)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ListLanguagesResponse{Languages: langs})
}

func (h *Handler) getCurrentConfig(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if h.svc == nil {
		writeServiceError(w, r, ErrNotImplemented)
		return
	}

	cfg, err := h.svc.GetActiveConfig(r.Context(), accountID, r.PathValue("id"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (h *Handler) createConfig(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if h.svc == nil {
		writeServiceError(w, r, ErrNotImplemented)
		return
	}

	var req CreateLanguageConfigRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeServiceError(w, r, err)
		return
	}

	cfg, err := h.svc.CreateConfig(
		r.Context(),
		accountID,
		r.PathValue("id"),
		r.Header.Get("Idempotency-Key"),
		req,
	)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, cfg)
}

func (h *Handler) listConfigHistory(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if h.svc == nil {
		writeServiceError(w, r, ErrNotImplemented)
		return
	}

	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 1 {
			writeServiceError(w, r, invalidRequest("limit"))
			return
		}
		limit = n
	}

	items, next, err := h.svc.ListConfigHistory(
		r.Context(),
		accountID,
		r.PathValue("id"),
		r.URL.Query().Get("cursor"),
		limit,
	)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	resp := ListLanguageConfigsResponse{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) requireAccount(r *http.Request) (string, error) {
	accountID, ok := h.accountID(r)
	if !ok || accountID == "" {
		return "", ErrUnauthenticated
	}
	return accountID, nil
}

func decodeJSONBody(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalidRequest("json body")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return invalidRequest("json body")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
