// Package controlplane adapts realtime lifecycle and signaling ports to HTTP.
package controlplane

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

const (
	defaultRoutePrefix      = "/realtime/v1"
	maxBodyBytes            = 1 << 20
	defaultReplayTTL        = 10 * time.Minute
	defaultReplayMaxEntries = 4096
)

var (
	ErrInvalidDependency = errors.New("invalid control-plane dependency")
	ErrInvalidRequest    = errors.New("invalid control-plane request")
	ErrTicketRequired    = errors.New("realtime ticket is required")
	ErrConfigSession     = errors.New("WebRTC config session mismatch")
	ErrReplayCapacity    = errors.New("control-plane replay capacity exhausted")
)

// Lifecycle is the realtime media lifecycle owned by session.LifecycleService.
type Lifecycle interface {
	Start(context.Context, session.StartRealtimeCommand) (session.RuntimeSnapshot, error)
	Stop(context.Context, session.StopRealtimeCommand) (session.RuntimeSnapshot, error)
	GetRuntimeState(context.Context, string) (session.RuntimeSnapshot, error)
}

// Signaling is the existing ticket-aware WebRTC signaling service boundary.
type Signaling interface {
	Offer(context.Context, string, string, webrtc.OfferRequest) (webrtc.OfferResponse, error)
	AddCandidates(context.Context, string, string, webrtc.CandidateRequest) (webrtc.CandidateResponse, error)
}

// ConfigReader returns the typed runtime WebRTC configuration for one session.
type ConfigReader interface {
	GetConfig(context.Context, string) (WebRTCConfig, error)
}

// WebRTCConfig is the public, transport-neutral runtime configuration response.
type WebRTCConfig struct {
	SessionID          string            `json:"session_id"`
	ExpiresAt          time.Time         `json:"expires_at"`
	ICEServers         []ICEServer       `json:"ice_servers"`
	ICETransportPolicy string            `json:"ice_transport_policy"`
	DataChannel        DataChannelConfig `json:"data_channel"`
	Audio              AudioConfig       `json:"audio"`
}

type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

type DataChannelConfig struct {
	Label   string `json:"label"`
	Ordered bool   `json:"ordered"`
}

type AudioConfig struct {
	UplinkCodec   string `json:"uplink_codec"`
	DownlinkCodec string `json:"downlink_codec"`
	SampleRateHz  int    `json:"sample_rate_hz"`
	Channels      int    `json:"channels"`
}

// Dependencies wires existing lifecycle, ticket, signaling, and config ports.
type Dependencies struct {
	Lifecycle        Lifecycle
	Signaling        Signaling
	Tickets          webrtc.TicketValidator
	Config           ConfigReader
	Now              func() time.Time
	ReplayTTL        time.Duration
	ReplayMaxEntries int
}

// Handler serves the realtime control-plane routes.
type Handler struct {
	lifecycle Lifecycle
	signaling Signaling
	tickets   webrtc.TicketValidator
	config    ConfigReader
	now       func() time.Time
	mux       *http.ServeMux

	replayMu         sync.Mutex
	replays          map[string]*replayRecord
	replayTTL        time.Duration
	replayMaxEntries int
}

type replayRecord struct {
	hash      string
	value     any
	err       error
	ready     chan struct{}
	expiresAt time.Time
}

// New validates dependencies and registers the default /realtime/v1 routes.
func New(dependencies Dependencies) (*Handler, error) {
	if dependencies.Lifecycle == nil || dependencies.Signaling == nil || dependencies.Tickets == nil || dependencies.Config == nil {
		return nil, ErrInvalidDependency
	}
	if dependencies.Now == nil {
		dependencies.Now = func() time.Time { return time.Now().UTC() }
	}
	if dependencies.ReplayTTL <= 0 {
		dependencies.ReplayTTL = defaultReplayTTL
	}
	if dependencies.ReplayMaxEntries <= 0 {
		dependencies.ReplayMaxEntries = defaultReplayMaxEntries
	}
	h := &Handler{
		lifecycle:        dependencies.Lifecycle,
		signaling:        dependencies.Signaling,
		tickets:          dependencies.Tickets,
		config:           dependencies.Config,
		now:              dependencies.Now,
		mux:              http.NewServeMux(),
		replays:          make(map[string]*replayRecord),
		replayTTL:        dependencies.ReplayTTL,
		replayMaxEntries: dependencies.ReplayMaxEntries,
	}
	h.registerRoutes(defaultRoutePrefix)
	return h, nil
}

func (h *Handler) registerRoutes(prefix string) {
	h.mux.HandleFunc("POST "+prefix+"/sessions/{session_id}/start", h.start)
	h.mux.HandleFunc("POST "+prefix+"/sessions/{session_id}/stop", h.stop)
	h.mux.HandleFunc("GET "+prefix+"/sessions/{session_id}/runtime", h.runtime)
	h.mux.HandleFunc("GET "+prefix+"/sessions/{session_id}/webrtc/config", h.configHandler)
	h.mux.HandleFunc("POST "+prefix+"/sessions/{session_id}/webrtc/offer", h.offer)
	h.mux.HandleFunc("POST "+prefix+"/sessions/{session_id}/ice-candidates", h.candidates)
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mux.ServeHTTP(writer, request)
}

type startRequest struct {
	TraceID   string `json:"trace_id"`
	StartedBy string `json:"started_by"`
}

type stopRequest struct {
	TraceID string `json:"trace_id"`
	Reason  string `json:"reason"`
}

func (h *Handler) start(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("session_id")
	if _, err := h.authorize(request.Context(), request, sessionID); err != nil {
		h.writeError(writer, request, err)
		return
	}
	idempotencyKey, err := requiredIdempotencyKey(request)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	var body startRequest
	if err := decodeJSON(request, &body, true); err != nil {
		h.writeError(writer, request, err)
		return
	}
	h.handleReplay(writer, request.Context(), "start\x00"+sessionID+"\x00"+idempotencyKey, body, func() (any, error) {
		return h.lifecycle.Start(request.Context(), session.StartRealtimeCommand{
			SessionID: sessionID, TraceID: body.TraceID, StartedBy: body.StartedBy,
		})
	})
}

func (h *Handler) stop(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("session_id")
	if _, err := h.authorize(request.Context(), request, sessionID); err != nil {
		h.writeError(writer, request, err)
		return
	}
	idempotencyKey, err := requiredIdempotencyKey(request)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	var body stopRequest
	if err := decodeJSON(request, &body, true); err != nil {
		h.writeError(writer, request, err)
		return
	}
	if body.Reason == "" {
		body.Reason = "user_requested"
	}
	h.handleReplay(writer, request.Context(), "stop\x00"+sessionID+"\x00"+idempotencyKey, body, func() (any, error) {
		return h.lifecycle.Stop(request.Context(), session.StopRealtimeCommand{
			SessionID: sessionID, TraceID: body.TraceID, Reason: body.Reason, EndedAt: h.now(),
		})
	})
}

func (h *Handler) runtime(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("session_id")
	if _, err := h.authorize(request.Context(), request, sessionID); err != nil {
		h.writeError(writer, request, err)
		return
	}
	snapshot, err := h.lifecycle.GetRuntimeState(request.Context(), sessionID)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	h.writeJSON(writer, http.StatusOK, snapshot)
}

func (h *Handler) configHandler(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("session_id")
	if _, err := h.authorize(request.Context(), request, sessionID); err != nil {
		h.writeError(writer, request, err)
		return
	}
	config, err := h.config.GetConfig(request.Context(), sessionID)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	if config.SessionID != sessionID {
		h.writeError(writer, request, ErrConfigSession)
		return
	}
	h.writeJSON(writer, http.StatusOK, config)
}

func (h *Handler) offer(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("session_id")
	token, err := h.authorize(request.Context(), request, sessionID)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	idempotencyKey, err := requiredIdempotencyKey(request)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	var body webrtc.OfferRequest
	if err := decodeJSON(request, &body, false); err != nil {
		h.writeError(writer, request, err)
		return
	}
	body.IdempotencyKey = idempotencyKey
	response, err := h.signaling.Offer(request.Context(), token, sessionID, body)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	h.writeJSON(writer, http.StatusOK, response)
}

func (h *Handler) candidates(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("session_id")
	token, err := h.authorize(request.Context(), request, sessionID)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	var body webrtc.CandidateRequest
	if err := decodeJSON(request, &body, false); err != nil {
		h.writeError(writer, request, err)
		return
	}
	response, err := h.signaling.AddCandidates(request.Context(), token, sessionID, body)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	h.writeJSON(writer, http.StatusOK, response)
}

func (h *Handler) authorize(ctx context.Context, request *http.Request, sessionID string) (string, error) {
	if sessionID == "" {
		return "", ErrInvalidRequest
	}
	token, err := bearerToken(request.Header.Get("Authorization"))
	if err != nil {
		return "", err
	}
	ticket, err := h.tickets.Validate(ctx, token, sessionID)
	if err != nil {
		return "", err
	}
	if ticket.SessionID != sessionID {
		return "", webrtc.ErrTicketSessionMismatch
	}
	if ticket.AccountID == "" {
		return "", webrtc.ErrTicketAccountRequired
	}
	if ticket.ExpiresAt.IsZero() || !ticket.ExpiresAt.After(h.now()) {
		return "", webrtc.ErrTicketExpired
	}
	return token, nil
}

func (h *Handler) handleReplay(writer http.ResponseWriter, ctx context.Context, key string, body any, operation func() (any, error)) {
	hash, err := bodyHash(body)
	if err != nil {
		h.writeError(writer, nil, err)
		return
	}
	record, owner, err := h.reserveReplay(key, hash)
	if err != nil {
		h.writeError(writer, nil, err)
		return
	}
	if !owner {
		select {
		case <-record.ready:
			if record.err != nil {
				h.writeError(writer, nil, record.err)
				return
			}
			h.writeJSON(writer, http.StatusOK, record.value)
		case <-ctx.Done():
			h.writeError(writer, nil, ctx.Err())
		}
		return
	}

	value, err := operation()
	h.replayMu.Lock()
	if err != nil {
		delete(h.replays, key)
		record.err = err
		close(record.ready)
		h.replayMu.Unlock()
		h.writeError(writer, nil, err)
		return
	}
	record.value = value
	record.expiresAt = h.now().Add(h.replayTTL)
	close(record.ready)
	h.replayMu.Unlock()
	h.writeJSON(writer, http.StatusOK, value)
}

func (h *Handler) reserveReplay(key, hash string) (*replayRecord, bool, error) {
	now := h.now()
	h.replayMu.Lock()
	defer h.replayMu.Unlock()
	if previous, ok := h.replays[key]; ok {
		if !previous.expiresAt.IsZero() && !previous.expiresAt.After(now) {
			delete(h.replays, key)
		} else {
			if previous.hash != hash {
				return nil, false, webrtc.ErrIdempotencyPayloadConflict
			}
			return previous, false, nil
		}
	}
	h.purgeExpiredReplaysLocked(now)
	if len(h.replays) >= h.replayMaxEntries {
		return nil, false, ErrReplayCapacity
	}
	record := &replayRecord{hash: hash, ready: make(chan struct{})}
	h.replays[key] = record
	return record, true, nil
}

func (h *Handler) purgeExpiredReplaysLocked(now time.Time) {
	for key, record := range h.replays {
		if record.expiresAt.IsZero() || record.expiresAt.After(now) {
			continue
		}
		delete(h.replays, key)
	}
}

func requiredIdempotencyKey(request *http.Request) (string, error) {
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if key == "" {
		return "", webrtc.ErrIdempotencyKeyRequired
	}
	return key, nil
}

func bearerToken(raw string) (string, error) {
	parts := strings.Fields(raw)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", ErrTicketRequired
	}
	return parts[1], nil
}

func decodeJSON(request *http.Request, target any, allowEmpty bool) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return ErrInvalidRequest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

func bodyHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("hash request body: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (h *Handler) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func (h *Handler) writeError(writer http.ResponseWriter, request *http.Request, err error) {
	status, code := mapError(err)
	if writer == nil {
		return
	}
	h.writeJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}

func mapError(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusInternalServerError, "internal_error"
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, webrtc.ErrSessionIDRequired),
		errors.Is(err, webrtc.ErrOfferSDPRequired), errors.Is(err, webrtc.ErrOfferTypeInvalid),
		errors.Is(err, webrtc.ErrIdempotencyKeyRequired), errors.Is(err, webrtc.ErrCandidateIDRequired),
		errors.Is(err, webrtc.ErrCandidateRequired):
		return http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrTicketRequired), errors.Is(err, webrtc.ErrRealtimeTokenRequired),
		errors.Is(err, webrtc.ErrTicketExpired), errors.Is(err, webrtc.ErrTicketSessionMismatch),
		errors.Is(err, webrtc.ErrTicketAccountRequired):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, session.ErrRuntimeNotFound), errors.Is(err, webrtc.ErrConnectionNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, session.ErrRuntimeCleanupRequired), errors.Is(err, session.ErrSessionNotCreated),
		errors.Is(err, webrtc.ErrIdempotencyPayloadConflict), errors.Is(err, webrtc.ErrConnectionAlreadyExists),
		errors.Is(err, webrtc.ErrConnectionClosing), errors.Is(err, webrtc.ErrCandidatesCompleted),
		errors.Is(err, ErrConfigSession):
		return http.StatusConflict, "conflict"
	case errors.Is(err, ErrReplayCapacity):
		return http.StatusServiceUnavailable, "replay_capacity_exhausted"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "request_timeout"
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "request_canceled"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

var _ http.Handler = (*Handler)(nil)
var _ Lifecycle = (*session.LifecycleService)(nil)
var _ Signaling = (*webrtc.SignalingService)(nil)
