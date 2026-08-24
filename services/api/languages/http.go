package languages

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
)

// AccountIDFromContext extracts the authenticated account ID set by trusted middleware.
type AccountIDFromContext func(r *http.Request) (accountID string, ok bool)

// Handler serves the language-configuration HTTP surface from issue #88 §3.
type Handler struct {
	svc         *Service
	accountID   AccountIDFromContext
	systemToken string
}

const systemTokenHeader = "X-Lingow-System-Token"

// NewHandler returns HTTP handlers backed by Service.
// accountID must read only middleware-injected identity (never client body fields).
func NewHandler(svc *Service, accountID AccountIDFromContext) *Handler {
	if accountID == nil {
		accountID = func(*http.Request) (string, bool) { return "", false }
	}
	return &Handler{svc: svc, accountID: accountID}
}

// ConfigureSystemCommands enables the fail-closed internal endpoint used by realtime command
// orchestration. An empty token keeps the endpoint registered but unavailable.
func (h *Handler) ConfigureSystemCommands(token string) {
	if h != nil {
		h.systemToken = strings.TrimSpace(token)
	}
}

// Register attaches account language routes behind caller authentication and the
// internal command route behind its fail-closed system-token boundary.
func (h *Handler) Register(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	if authenticate == nil {
		panic("language authentication middleware is required")
	}
	mux.Handle("GET /api/v1/languages", authenticate(http.HandlerFunc(h.listLanguages)))
	mux.Handle("GET /api/v1/account/automatic-delivery-readiness", authenticate(http.HandlerFunc(h.automaticDeliveryReadiness)))
	mux.Handle("GET /api/v1/voice-sessions/{id}/language-config", authenticate(http.HandlerFunc(h.getCurrentConfig)))
	mux.Handle("POST /api/v1/voice-sessions/{id}/language-configs", authenticate(http.HandlerFunc(h.createConfig)))
	mux.Handle("GET /api/v1/voice-sessions/{id}/language-configs", authenticate(http.HandlerFunc(h.listConfigHistory)))
	mux.Handle("GET /internal/v1/voice-sessions/{id}/language-config", http.HandlerFunc(h.getCurrentConfigForCommand))
	mux.Handle("POST /internal/v1/voice-sessions/{id}/language-config", http.HandlerFunc(h.configureFromCommand))
}

func (h *Handler) getCurrentConfigForCommand(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.svc == nil || h.systemToken == "" ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get(systemTokenHeader)), []byte(h.systemToken)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	snapshot, err := h.svc.GetCurrentConfig(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if len(snapshot.LanguagePairs) != 2 ||
		snapshot.LanguagePairs[0].Source != snapshot.LanguagePairs[1].Target ||
		snapshot.LanguagePairs[0].Target != snapshot.LanguagePairs[1].Source {
		writeServiceError(w, r, languagesv1.ErrInvalidCommandConfigSnapshot)
		return
	}
	routes, err := normalizeOutputRoutes(snapshot.LanguagePairs, snapshot.OutputRoutes)
	if err != nil {
		writeServiceError(w, r, languagesv1.ErrInvalidCommandConfigSnapshot)
		return
	}
	primary := snapshot.LanguagePairs[0]
	result := languagesv1.CommandConfigSnapshot{
		SessionID: snapshot.SessionID, SourceLanguage: primary.Source, TargetLanguage: primary.Target,
		OutputMode: outputModeForRoutes(routes), Version: snapshot.Version,
	}
	if err := result.Validate(); err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) configureFromCommand(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.svc == nil || h.systemToken == "" ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get(systemTokenHeader)), []byte(h.systemToken)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var request languagesv1.CommandConfigRequest
	if err := decodeJSONBody(r, &request); err != nil || request.Validate() != nil || request.SessionID != r.PathValue("id") {
		writeServiceError(w, r, invalidRequest("command language configuration"))
		return
	}
	accountID, err := h.svc.sessions.GetOwnerAccountID(r.Context(), request.SessionID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	createRequest := CreateLanguageConfigRequest{
		Languages: []LanguagePair{
			{Source: request.SourceLanguage, Target: request.TargetLanguage},
			{Source: request.TargetLanguage, Target: request.SourceLanguage},
		},
		OutputRoutes:    commandOutputRoutes(request),
		ExpectedVersion: request.ExpectedVersion,
	}
	fingerprintRequest := createRequest
	fingerprintRequest.ExpectedVersion = nil
	config, err := h.svc.createConfig(
		r.Context(), accountID, request.SessionID,
		commandIdempotencyKey(request.SessionID, request.CommandID),
		createRequest, requestFingerprint(fingerprintRequest),
	)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if config.Status != StatusActive {
		writeServiceError(w, r, ErrStaleCommand)
		return
	}
	if request.OutputMode == "" && request.ExpectedVersion == nil {
		// Older realtime deployments reject unknown response fields. Preserve the
		// original v1 response until they have been upgraded.
		writeJSON(w, http.StatusOK, struct {
			SessionID string `json:"session_id"`
			CommandID string `json:"command_id"`
			Version   int    `json:"version"`
		}{SessionID: request.SessionID, CommandID: request.CommandID, Version: config.Version})
		return
	}
	writeJSON(w, http.StatusOK, languagesv1.CommandConfigResult{
		SessionID: request.SessionID, CommandID: request.CommandID,
		SourceLanguage: request.SourceLanguage, TargetLanguage: request.TargetLanguage,
		OutputMode: config.OutputMode, Version: config.Version,
	})
}

func commandOutputRoutes(request languagesv1.CommandConfigRequest) []OutputRoute {
	if request.OutputMode != languagesv1.InterpretationOutputModeSingle {
		return nil
	}
	return []OutputRoute{
		{TargetLanguage: request.TargetLanguage, TTSEnabled: true},
		{TargetLanguage: request.SourceLanguage, DeliveryEnabled: true},
	}
}

// commandIdempotencyKey scopes command retries to one session while keeping the
// existing language-config idempotency column's 128-byte limit.
func commandIdempotencyKey(sessionID, commandID string) string {
	payload, _ := json.Marshal([2]string{sessionID, commandID})
	digest := sha256.Sum256(payload)
	return "command_" + hex.EncodeToString(digest[:])
}

func (h *Handler) automaticDeliveryReadiness(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	if h.svc == nil {
		writeServiceError(w, r, ErrNotImplemented)
		return
	}
	ready, err := h.svc.AutomaticDeliveryReady(r.Context(), accountID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, AutomaticDeliveryReadinessResponse{Ready: ready})
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

const maxRequestBodyBytes = 1 << 20

func decodeJSONBody(r *http.Request, target any) error {
	defer r.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBodyBytes+1))
	if err != nil || len(payload) > maxRequestBodyBytes {
		return invalidRequest("json body")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
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
