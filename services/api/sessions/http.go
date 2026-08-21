package sessions

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

const maxHTTPBodyBytes = 1 << 20

// AccountIDFromRequest extracts the account ID written by trusted auth middleware.
type AccountIDFromRequest func(*http.Request) (string, bool)

// UseCases is the application boundary consumed by the HTTP handler.
type UseCases interface {
	Create(context.Context, CreateInput) (VoiceSession, error)
	Start(context.Context, StartInput) (VoiceSession, error)
	End(context.Context, EndInput) (VoiceSession, error)
	GetDetail(context.Context, DetailInput) (VoiceSessionDetail, error)
	GetState(context.Context, DetailInput) (StateSnapshot, error)
	List(context.Context, ListInput) (ListPage, error)
}

// ModeUseCases is an optional capability so existing Session handlers can be
// constructed without a realtime dependency while the runtime is disabled.
type ModeUseCases interface {
	GetMode(context.Context, DetailInput) (ModeSnapshot, error)
	SwitchMode(context.Context, SwitchModeInput) (ModeSwitchResult, error)
}

// RealtimeTicket is the browser-facing mint response for WebRTC signaling.
type RealtimeTicket struct {
	Ticket    string    `json:"ticket"`
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RealtimeTicketMinter issues short-lived realtime tickets for session owners.
type RealtimeTicketMinter interface {
	MintRealtimeTicket(ctx context.Context, accountID, sessionID string) (RealtimeTicket, error)
}

// DeviceSessionAccess supplies the device identity already validated by the
// route middleware and enforces the device-to-session relation. It deliberately
// has no account-management capability.
type DeviceSessionAccess struct {
	DeviceID func(*http.Request) (string, bool)
	Owns     func(context.Context, string, string, string) error
}

// Handler exposes Issue #86's public voice-session HTTP contract.
type Handler struct {
	service   UseCases
	modes     ModeUseCases
	accountID AccountIDFromRequest
	tickets   RealtimeTicketMinter
}

// NewHandler wires the HTTP boundary. The account extractor must read only
// middleware-injected identity; request bodies and headers are not authority.
func NewHandler(service UseCases, accountID AccountIDFromRequest) *Handler {
	if accountID == nil {
		accountID = func(*http.Request) (string, bool) { return "", false }
	}
	return &Handler{service: service, accountID: accountID}
}

// WithRealtimeTickets enables POST .../realtime-ticket for browser WebRTC join.
func (h *Handler) WithRealtimeTickets(tickets RealtimeTicketMinter) *Handler {
	if h == nil {
		return nil
	}
	h.tickets = tickets
	return h
}

// WithRealtimeModes supplies account-scoped mode query and compare-and-switch
// behavior. The routes remain mounted without this optional capability so a
// disabled runtime returns the stable not_implemented contract.
func (h *Handler) WithRealtimeModes(modes ModeUseCases) *Handler {
	if h == nil {
		return nil
	}
	h.modes = modes
	return h
}

// Register attaches voice-session lifecycle routes behind authentication.
func (h *Handler) Register(mux *http.ServeMux, authenticate func(http.Handler) http.Handler) {
	if authenticate == nil {
		panic("sessions authentication middleware is required")
	}
	mux.Handle("POST /api/v1/voice-sessions", authenticate(http.HandlerFunc(h.create)))
	mux.Handle("POST /api/v1/voice-sessions/{id}/start", authenticate(http.HandlerFunc(h.start)))
	mux.Handle("POST /api/v1/voice-sessions/{id}/end", authenticate(http.HandlerFunc(h.end)))
	mux.Handle("GET /api/v1/voice-sessions/{id}", authenticate(http.HandlerFunc(h.detail)))
	mux.Handle("GET /api/v1/voice-sessions", authenticate(http.HandlerFunc(h.list)))
	mux.Handle("GET /api/v1/voice-sessions/{id}/state", authenticate(http.HandlerFunc(h.state)))
	if h.tickets != nil {
		mux.Handle(
			"POST /api/v1/voice-sessions/{id}/realtime-ticket",
			authenticate(http.HandlerFunc(h.mintRealtimeTicket)),
		)
	}
	mux.Handle("GET /api/v1/voice-sessions/{id}/mode", authenticate(http.HandlerFunc(h.modeState)))
	mux.Handle("POST /api/v1/voice-sessions/{id}/mode", authenticate(http.HandlerFunc(h.switchMode)))
}

// RegisterDevice attaches the constrained device lifecycle surface. The
// passed authenticator must inject the bound account identity before this
// handler runs; device ownership is checked again for existing sessions.
func (h *Handler) RegisterDevice(mux *http.ServeMux, authenticate func(http.Handler) http.Handler, access DeviceSessionAccess) {
	if authenticate == nil || access.DeviceID == nil || access.Owns == nil {
		panic("device session authentication is required")
	}
	mux.Handle("POST /api/v1/device/voice-sessions", authenticate(http.HandlerFunc(h.deviceCreate(access))))
	mux.Handle("POST /api/v1/device/voice-sessions/{id}/start", authenticate(http.HandlerFunc(h.deviceExisting(access, h.start))))
	mux.Handle("POST /api/v1/device/voice-sessions/{id}/end", authenticate(http.HandlerFunc(h.deviceExisting(access, h.end))))
	if h.tickets != nil {
		mux.Handle("POST /api/v1/device/voice-sessions/{id}/realtime-ticket", authenticate(http.HandlerFunc(h.deviceExisting(access, h.mintRealtimeTicket))))
	}
}

func (h *Handler) deviceCreate(access DeviceSessionAccess) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID, err := h.requireAccount(r)
		if err != nil {
			writeHTTPError(w, r, err)
			return
		}
		deviceID, ok := access.DeviceID(r)
		if !ok || deviceID == "" {
			writeHTTPError(w, r, ErrUnauthorized)
			return
		}
		if h.service == nil {
			writeHTTPError(w, r, ErrNotImplemented)
			return
		}
		var body createRequest
		if err := decodeHTTPJSON(r, &body); err != nil {
			writeHTTPError(w, r, err)
			return
		}
		session, err := h.service.Create(r.Context(), CreateInput{AccountID: accountID, DeviceID: deviceID, AudioConfig: body.AudioConfig, Capabilities: body.Capabilities, IdempotencyKey: r.Header.Get("Idempotency-Key"), RequestHash: canonicalHash("voice-sessions.create", body)})
		if err != nil {
			writeHTTPError(w, r, err)
			return
		}
		writeHTTPJSON(w, http.StatusCreated, session)
	}
}

func (h *Handler) deviceExisting(access DeviceSessionAccess, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID, err := h.requireAccount(r)
		if err != nil {
			writeHTTPError(w, r, err)
			return
		}
		deviceID, ok := access.DeviceID(r)
		if !ok || deviceID == "" {
			writeHTTPError(w, r, ErrUnauthorized)
			return
		}
		if err := access.Owns(r.Context(), deviceID, accountID, r.PathValue("id")); err != nil {
			writeHTTPError(w, r, err)
			return
		}
		next(w, r)
	}
}

type createRequest struct {
	AudioConfig  *AudioConfig `json:"audio_config,omitempty"`
	Capabilities Capabilities `json:"capabilities"`
}

type startRequest struct {
	InitialMode realtimev1.Mode `json:"initial_mode,omitempty"`
}

type endRequest struct {
	Reason EndReason `json:"reason,omitempty"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if h.service == nil {
		writeHTTPError(w, r, ErrNotImplemented)
		return
	}
	var body createRequest
	if err := decodeHTTPJSON(r, &body); err != nil {
		writeHTTPError(w, r, err)
		return
	}
	input := CreateInput{
		AccountID:      accountID,
		AudioConfig:    body.AudioConfig,
		Capabilities:   body.Capabilities,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		RequestHash:    canonicalHash("voice-sessions.create", body),
	}
	session, err := h.service.Create(r.Context(), input)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	writeHTTPJSON(w, http.StatusCreated, session)
}

func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if h.service == nil {
		writeHTTPError(w, r, ErrNotImplemented)
		return
	}
	body := startRequest{}
	if err := decodeOptionalHTTPJSON(r, &body); err != nil {
		writeHTTPError(w, r, err)
		return
	}
	body.InitialMode = body.InitialMode.OrLegacyDefault()
	if !body.InitialMode.Valid() {
		writeHTTPError(w, r, ErrInvalidRequest)
		return
	}
	sessionID := r.PathValue("id")
	requestHash := canonicalHash("voice-sessions.start", struct {
		SessionID string `json:"session_id"`
	}{SessionID: sessionID})
	if body.InitialMode != realtimev1.ModeInterpretation {
		requestHash = canonicalHash("voice-sessions.start", struct {
			SessionID   string          `json:"session_id"`
			InitialMode realtimev1.Mode `json:"initial_mode"`
		}{SessionID: sessionID, InitialMode: body.InitialMode})
	}
	input := StartInput{
		AccountID:      accountID,
		SessionID:      sessionID,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		RequestHash:    requestHash,
		TraceID:        requestIDFromHTTP(r),
		StartedBy:      accountID,
		InitialMode:    body.InitialMode,
	}
	session, err := h.service.Start(r.Context(), input)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, session)
}

func (h *Handler) end(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if h.service == nil {
		writeHTTPError(w, r, ErrNotImplemented)
		return
	}
	body := endRequest{Reason: EndReasonUserRequested}
	if err := decodeOptionalHTTPJSON(r, &body); err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if body.Reason == "" {
		body.Reason = EndReasonUserRequested
	}
	sessionID := r.PathValue("id")
	input := EndInput{
		AccountID:      accountID,
		SessionID:      sessionID,
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		RequestHash: canonicalHash("voice-sessions.end", struct {
			SessionID string    `json:"session_id"`
			Reason    EndReason `json:"reason"`
		}{SessionID: sessionID, Reason: body.Reason}),
		TraceID: requestIDFromHTTP(r),
		Reason:  body.Reason,
	}
	session, err := h.service.End(r.Context(), input)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, session)
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if h.service == nil {
		writeHTTPError(w, r, ErrNotImplemented)
		return
	}
	detail, err := h.service.GetDetail(r.Context(), DetailInput{
		AccountID: accountID,
		SessionID: r.PathValue("id"),
	})
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, detail)
}

func (h *Handler) state(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if h.service == nil {
		writeHTTPError(w, r, ErrNotImplemented)
		return
	}
	state, err := h.service.GetState(r.Context(), DetailInput{
		AccountID: accountID,
		SessionID: r.PathValue("id"),
	})
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, state)
}

func (h *Handler) modeState(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if h.modes == nil {
		writeHTTPError(w, r, ErrNotImplemented)
		return
	}
	state, err := h.modes.GetMode(r.Context(), DetailInput{
		AccountID: accountID,
		SessionID: r.PathValue("id"),
	})
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, state)
}

type switchModeRequest struct {
	RuntimeInstanceID  string `json:"runtime_instance_id"`
	ExpectedGeneration int64  `json:"expected_generation"`
	TargetMode         Mode   `json:"target_mode"`
}

func (h *Handler) switchMode(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if h.modes == nil {
		writeHTTPError(w, r, ErrNotImplemented)
		return
	}
	var body switchModeRequest
	if err := decodeHTTPJSON(r, &body); err != nil {
		writeHTTPError(w, r, err)
		return
	}
	operationID := r.Header.Get("Idempotency-Key")
	if body.RuntimeInstanceID == "" || body.ExpectedGeneration < 1 ||
		!body.TargetMode.Valid() || operationID == "" ||
		len(operationID) > maxIdempotencyKeyLength {
		writeHTTPError(w, r, ErrInvalidRequest)
		return
	}
	requestID := requestIDFromHTTP(r)
	if len(requestID) > maxRequestIDLength {
		r.Header.Del("X-Request-ID")
		writeHTTPError(w, r, ErrInvalidRequest)
		return
	}
	traceID := modeOperationTraceID(r.PathValue("id"), operationID)
	result, err := h.modes.SwitchMode(r.Context(), SwitchModeInput{
		AccountID:          accountID,
		SessionID:          r.PathValue("id"),
		RuntimeInstanceID:  body.RuntimeInstanceID,
		OperationID:        operationID,
		TraceID:            traceID,
		ExpectedGeneration: body.ExpectedGeneration,
		TargetMode:         body.TargetMode,
	})
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, result)
}

func (h *Handler) mintRealtimeTicket(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if h.tickets == nil {
		writeHTTPError(w, r, ErrNotImplemented)
		return
	}
	if err := rejectNonEmptyBody(r); err != nil {
		writeHTTPError(w, r, err)
		return
	}
	ticket, err := h.tickets.MintRealtimeTicket(r.Context(), accountID, r.PathValue("id"))
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, ticket)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	accountID, err := h.requireAccount(r)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	if h.service == nil {
		writeHTTPError(w, r, ErrNotImplemented)
		return
	}
	input := ListInput{
		AccountID: accountID,
		Cursor:    r.URL.Query().Get("cursor"),
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		status := Status(raw)
		input.Status = &status
	}
	limit, err := parseListLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	input.Limit = limit
	page, err := h.service.List(r.Context(), input)
	if err != nil {
		writeHTTPError(w, r, err)
		return
	}
	writeHTTPJSON(w, http.StatusOK, page)
}

func (h *Handler) requireAccount(r *http.Request) (string, error) {
	accountID, ok := h.accountID(r)
	if !ok || accountID == "" {
		return "", ErrUnauthorized
	}
	return accountID, nil
}

func parseListLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > maxListLimit {
		return 0, ErrInvalidRequest
	}
	return limit, nil
}

func decodeHTTPJSON(r *http.Request, target any) error {
	body, err := readHTTPBody(r)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

func decodeOptionalHTTPJSON(r *http.Request, target any) error {
	body, err := readHTTPBody(r)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

func rejectNonEmptyBody(r *http.Request) error {
	body, err := readHTTPBody(r)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) != 0 {
		return ErrInvalidRequest
	}
	return nil
}

func readHTTPBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBodyBytes+1))
	if err != nil || len(body) > maxHTTPBodyBytes {
		return nil, ErrInvalidRequest
	}
	return body, nil
}

func canonicalHash(operation string, value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("sessions canonical request hash: %v", err))
	}
	sum := sha256.Sum256(append([]byte(operation+"\x00"), payload...))
	return hex.EncodeToString(sum[:])
}

type httpErrorEnvelope struct {
	Error httpErrorDetail `json:"error"`
}

type httpErrorDetail struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

func writeHTTPJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := statusCodeForError(err)
	writeHTTPJSON(w, status, httpErrorEnvelope{
		Error: httpErrorDetail{
			Code:      string(code),
			Message:   message,
			RequestID: requestIDFromHTTP(r),
			Retryable: false,
			Details:   map[string]any{},
		},
	})
}

func statusCodeForError(err error) (int, ErrorCode, string) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return http.StatusBadRequest, CodeInvalidRequest, "invalid request"
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized, CodeUnauthorized, "authentication required"
	case errors.Is(err, ErrVoiceSessionNotFound):
		return http.StatusNotFound, CodeVoiceSessionNotFound, "voice session not found"
	case errors.Is(err, ErrIdempotencyKeyConflict):
		return http.StatusConflict, CodeIdempotencyKeyConflict, "idempotency key was reused with a different request"
	case errors.Is(err, ErrSessionStartInProgress):
		return http.StatusConflict, CodeSessionStartInProgress, "voice session start is already in progress"
	case errors.Is(err, ErrSessionStateConflict):
		return http.StatusConflict, CodeSessionStateConflict, "voice session state does not allow this operation"
	case errors.Is(err, ErrLanguageConfigNotReady):
		return http.StatusConflict, CodeLanguageConfigNotReady, "language config is not ready"
	case errors.Is(err, ErrWebRTCNotReady):
		return http.StatusConflict, CodeWebRTCNotReady, "WebRTC connection is not ready"
	case errors.Is(err, ErrRealtimeAlreadyRunning):
		return http.StatusConflict, CodeRealtimeAlreadyRunning, "realtime pipeline is already running"
	case errors.Is(err, ErrUnsupportedAudio):
		return http.StatusUnprocessableEntity, CodeUnsupportedAudio, "audio config is not supported"
	case errors.Is(err, ErrRealtimeStartFailed):
		return http.StatusServiceUnavailable, CodeRealtimeStartFailed, "realtime pipeline failed to start"
	case errors.Is(err, ErrRealtimeStopFailed):
		return http.StatusServiceUnavailable, CodeRealtimeStopFailed, "realtime pipeline failed to stop"
	case errors.Is(err, ErrRuntimeUnavailable):
		return http.StatusServiceUnavailable, CodeRuntimeUnavailable, "runtime state is unavailable"
	case errors.Is(err, ErrWebRTCUnavailable):
		return http.StatusServiceUnavailable, CodeWebRTCUnavailable, "WebRTC state is unavailable"
	case errors.Is(err, ErrModeNotAvailable):
		return http.StatusUnprocessableEntity, CodeModeNotAvailable, "requested mode is not available"
	case errors.Is(err, ErrModeGenerationConflict):
		return http.StatusConflict, CodeModeGenerationConflict, "mode generation does not match"
	case errors.Is(err, ErrModeRuntimeMismatch):
		return http.StatusConflict, CodeModeRuntimeMismatch, "mode runtime instance does not match"
	case errors.Is(err, ErrModeOperationConflict):
		return http.StatusConflict, CodeModeOperationConflict, "mode operation conflicts with a previous request"
	case errors.Is(err, ErrModeUnavailable):
		return http.StatusServiceUnavailable, CodeModeUnavailable, "mode state is unavailable"
	case errors.Is(err, ErrNotImplemented):
		return http.StatusNotImplemented, CodeNotImplemented, "voice session dependency is not implemented yet"
	default:
		return http.StatusInternalServerError, ErrorCode("internal_error"), "internal error"
	}
}

func requestIDFromHTTP(r *http.Request) string {
	if requestID := r.Header.Get("X-Request-ID"); requestID != "" {
		return requestID
	}
	requestID := newHTTPRequestID()
	// Persist the generated value on this request so downstream commands and a
	// later error response always use the same trace identity.
	r.Header.Set("X-Request-ID", requestID)
	return requestID
}

func modeOperationTraceID(sessionID, operationID string) string {
	// Realtime includes TraceID when comparing an OperationID replay. Derive the
	// command trace from durable identities so a client's per-attempt HTTP
	// request ID cannot turn an otherwise identical retry into a conflict.
	digest := sha256.Sum256([]byte("voice-sessions.switch-mode\x00" + sessionID + "\x00" + operationID))
	return "req_mode_" + hex.EncodeToString(digest[:12])
}

var fallbackHTTPRequestIDSequence atomic.Uint64

func newHTTPRequestID() string {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err == nil {
		return "req_" + hex.EncodeToString(random)
	}
	return fmt.Sprintf(
		"req_%d_%d",
		time.Now().UTC().UnixNano(),
		fallbackHTTPRequestIDSequence.Add(1),
	)
}
