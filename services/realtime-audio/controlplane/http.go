// Package controlplane adapts realtime lifecycle and signaling ports to HTTP.
package controlplane

import (
	"bytes"
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

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

const (
	defaultRoutePrefix                = "/realtime/v1"
	maxBodyBytes                      = 1 << 20
	maxIdempotencyKeyBytes            = 256
	defaultReplayTTL                  = 10 * time.Minute
	defaultReplayMaxEntries           = 4096
	defaultReplayMaxEntriesPerSession = 64
)

var (
	ErrInvalidDependency          = errors.New("invalid control-plane dependency")
	ErrInvalidRequest             = errors.New("invalid control-plane request")
	ErrTicketRequired             = errors.New("realtime ticket is required")
	ErrConfigSession              = errors.New("WebRTC config session mismatch")
	ErrReplayCapacity             = errors.New("control-plane replay capacity exhausted")
	ErrIdempotencyKeyTooLong      = errors.New("idempotency key exceeds maximum length")
	ErrFallbackPlaybackInProgress = errors.New("fallback playback is already in progress")
)

// Lifecycle is the realtime media lifecycle owned by session.LifecycleService.
type Lifecycle interface {
	Start(context.Context, session.StartRealtimeCommand) (session.RuntimeSnapshot, error)
	Stop(context.Context, session.StopRealtimeCommand) (session.RuntimeSnapshot, error)
	GetRuntimeState(context.Context, string) (session.RuntimeSnapshot, error)
}

// FallbackPlayback accepts one immutable translated-text snapshot for media
// playback. Implementations must deduplicate by request.OperationID.
type FallbackPlayback interface {
	PlayFallback(context.Context, realtimev1.FallbackPlaybackRequest) error
}

// FallbackPlaybackClaimStatus reports whether a caller owns, is waiting on,
// or is replaying one durable fallback operation.
type FallbackPlaybackClaimStatus string

const (
	FallbackPlaybackClaimed    FallbackPlaybackClaimStatus = "claimed"
	FallbackPlaybackProcessing FallbackPlaybackClaimStatus = "processing"
	FallbackPlaybackAccepted   FallbackPlaybackClaimStatus = "accepted"
)

// FallbackPlaybackClaim describes durable ownership of one immutable fallback
// command before the media side effect starts.
type FallbackPlaybackClaim struct {
	Status FallbackPlaybackClaimStatus
	Token  string
}

// FallbackPlaybackReplayStore atomically claims fallback operations before
// media I/O and resolves the claim after playback outcome is known.
type FallbackPlaybackReplayStore interface {
	Claim(context.Context, string, string, string) (FallbackPlaybackClaim, error)
	Complete(context.Context, string, string, string, string) error
	Abort(context.Context, string, string, string, string) error
}

type fallbackPlaybackNotStarted interface {
	FallbackPlaybackNotStarted()
}

// Signaling is the existing ticket-aware WebRTC signaling service boundary.
type Signaling interface {
	Offer(context.Context, string, string, webrtc.OfferRequest) (webrtc.OfferResponse, error)
	AddCandidates(context.Context, string, string, webrtc.CandidateRequest) (webrtc.CandidateResponse, error)
}

// ConnectionReader returns the current WebRTC transport fact without exposing
// the manager's mutation or in-memory ownership APIs across the HTTP boundary.
type ConnectionReader interface {
	GetCurrent(context.Context, string) (realtimev1.ConnectionSnapshot, error)
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
	Lifecycle                  Lifecycle
	Fallback                   FallbackPlayback
	FallbackReplays            FallbackPlaybackReplayStore
	Signaling                  Signaling
	Connections                ConnectionReader
	Tickets                    webrtc.TicketValidator
	Config                     ConfigReader
	Now                        func() time.Time
	ReplayTTL                  time.Duration
	ReplayMaxEntries           int
	ReplayMaxEntriesPerSession int
}

// Handler serves the realtime control-plane routes.
type Handler struct {
	lifecycle       Lifecycle
	fallback        FallbackPlayback
	fallbackReplays FallbackPlaybackReplayStore
	signaling       Signaling
	connections     ConnectionReader
	tickets         webrtc.TicketValidator
	config          ConfigReader
	now             func() time.Time
	mux             *http.ServeMux

	replayMu                   sync.Mutex
	replays                    map[string]*replayRecord
	replayEntriesBySession     map[string]int
	replayTTL                  time.Duration
	replayMaxEntries           int
	replayMaxEntriesPerSession int
}

type replayRecord struct {
	sessionID string
	hash      string
	value     any
	err       error
	ready     chan struct{}
	expiresAt time.Time
}

// New validates dependencies and registers the default /realtime/v1 routes.
func New(dependencies Dependencies) (*Handler, error) {
	if dependencies.Lifecycle == nil || dependencies.Signaling == nil ||
		dependencies.Connections == nil || dependencies.Tickets == nil ||
		dependencies.Config == nil {
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
	if dependencies.ReplayMaxEntriesPerSession <= 0 {
		dependencies.ReplayMaxEntriesPerSession = defaultReplayMaxEntriesPerSession
	}
	if dependencies.ReplayMaxEntriesPerSession > dependencies.ReplayMaxEntries {
		dependencies.ReplayMaxEntriesPerSession = dependencies.ReplayMaxEntries
	}
	h := &Handler{
		lifecycle:                  dependencies.Lifecycle,
		fallback:                   dependencies.Fallback,
		fallbackReplays:            dependencies.FallbackReplays,
		signaling:                  dependencies.Signaling,
		connections:                dependencies.Connections,
		tickets:                    dependencies.Tickets,
		config:                     dependencies.Config,
		now:                        dependencies.Now,
		mux:                        http.NewServeMux(),
		replays:                    make(map[string]*replayRecord),
		replayEntriesBySession:     make(map[string]int),
		replayTTL:                  dependencies.ReplayTTL,
		replayMaxEntries:           dependencies.ReplayMaxEntries,
		replayMaxEntriesPerSession: dependencies.ReplayMaxEntriesPerSession,
	}
	h.registerRoutes(defaultRoutePrefix)
	return h, nil
}

func (h *Handler) registerRoutes(prefix string) {
	h.mux.HandleFunc("POST "+prefix+"/sessions/{session_id}/start", h.start)
	h.mux.HandleFunc("POST "+prefix+"/sessions/{session_id}/stop", h.stop)
	h.mux.HandleFunc("POST "+prefix+"/sessions/{session_id}/fallback-playback", h.fallbackPlayback)
	h.mux.HandleFunc("GET "+prefix+"/sessions/{session_id}/runtime", h.runtime)
	h.mux.HandleFunc("GET "+prefix+"/sessions/{session_id}/connection", h.connection)
	h.mux.HandleFunc("GET "+prefix+"/sessions/{session_id}/webrtc/config", h.configHandler)
	h.mux.HandleFunc("POST "+prefix+"/sessions/{session_id}/webrtc/offer", h.offer)
	h.mux.HandleFunc("POST "+prefix+"/sessions/{session_id}/ice-candidates", h.candidates)
}

func (h *Handler) fallbackPlayback(writer http.ResponseWriter, request *http.Request) {
	if h.fallback == nil {
		h.writeError(writer, request, ErrInvalidDependency)
		return
	}
	sessionID := request.PathValue("session_id")
	if _, err := h.authorize(request.Context(), request, sessionID); err != nil {
		h.writeError(writer, request, err)
		return
	}
	if _, err := requiredIdempotencyKey(request); err != nil {
		h.writeError(writer, request, err)
		return
	}
	var body realtimev1.FallbackPlaybackRequest
	if err := decodeJSON(request, &body, false); err != nil {
		h.writeError(writer, request, err)
		return
	}
	if strings.TrimSpace(body.OperationID) == "" || strings.TrimSpace(body.SessionID) != sessionID ||
		strings.TrimSpace(body.TurnID) == "" || strings.TrimSpace(body.TargetLanguage) == "" ||
		strings.TrimSpace(body.TranslatedText) == "" || body.LanguageConfigVersion < 1 || strings.TrimSpace(body.TraceID) == "" {
		h.writeError(writer, request, ErrInvalidRequest)
		return
	}
	payloadHash, err := bodyHash(body)
	if err != nil {
		h.writeError(writer, request, err)
		return
	}
	replayKey := "fallback\x00" + sessionID + "\x00" + body.OperationID
	h.handleReplayStatus(writer, request.Context(), sessionID, replayKey, body, http.StatusAccepted,
		func() (any, error) {
			claimToken := ""
			if h.fallbackReplays != nil {
				claim, err := h.fallbackReplays.Claim(request.Context(), sessionID, body.OperationID, payloadHash)
				if err != nil {
					return nil, err
				}
				switch claim.Status {
				case FallbackPlaybackClaimed:
					claimToken = claim.Token
				case FallbackPlaybackProcessing:
					return nil, ErrFallbackPlaybackInProgress
				case FallbackPlaybackAccepted:
					return realtimev1.FallbackPlaybackReceipt{OperationID: body.OperationID, Status: realtimev1.FallbackPlaybackAlreadyAccepted}, nil
				default:
					return nil, ErrInvalidDependency
				}
			}
			if err := h.fallback.PlayFallback(request.Context(), body); err != nil {
				if h.fallbackReplays != nil && claimToken != "" && isFallbackPlaybackNotStarted(err) {
					if abortErr := h.fallbackReplays.Abort(request.Context(), sessionID, body.OperationID, payloadHash, claimToken); abortErr != nil {
						return nil, errors.Join(err, abortErr)
					}
				}
				return nil, err
			}
			if h.fallbackReplays != nil {
				if err := h.fallbackReplays.Complete(request.Context(), sessionID, body.OperationID, payloadHash, claimToken); err != nil {
					return nil, err
				}
			}
			return realtimev1.FallbackPlaybackReceipt{OperationID: body.OperationID, Status: realtimev1.FallbackPlaybackAccepted}, nil
		}, func(value any, replay bool) any {
			receipt, ok := value.(realtimev1.FallbackPlaybackReceipt)
			if !ok {
				return value
			}
			if replay {
				receipt.Status = realtimev1.FallbackPlaybackAlreadyAccepted
			}
			return receipt
		})
}

func isFallbackPlaybackNotStarted(err error) bool {
	var marker fallbackPlaybackNotStarted
	return errors.As(err, &marker)
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mux.ServeHTTP(writer, request)
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
	var body realtimev1.StartRequest
	if err := decodeJSON(request, &body, true); err != nil {
		h.writeError(writer, request, err)
		return
	}
	if strings.TrimSpace(body.OperationID) == "" {
		h.writeError(writer, request, session.ErrStartOperationIDRequired)
		return
	}
	traceID := strings.TrimSpace(body.TraceID)
	if traceID == "" {
		traceID = body.OperationID
	}
	h.handleReplay(writer, request.Context(), sessionID, "start\x00"+sessionID+"\x00"+idempotencyKey, body, func() (any, error) {
		return h.lifecycle.Start(request.Context(), session.StartRealtimeCommand{
			SessionID: sessionID, OperationID: body.OperationID,
			TraceID: traceID, StartedBy: body.StartedBy,
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
	var body realtimev1.StopRequest
	if err := decodeJSON(request, &body, false); err != nil {
		h.writeError(writer, request, err)
		return
	}
	if !validStopReason(body.Reason) || body.EndedAt.IsZero() {
		h.writeError(writer, request, ErrInvalidRequest)
		return
	}
	// Replay identity matches controlplane.Client.Stop: key is stop:<reason>, and the
	// hashed body deliberately excludes TraceID/EndedAt so End retries may refresh
	// audit metadata without hitting an idempotency payload conflict.
	replayBody := struct {
		Reason string `json:"reason"`
	}{Reason: body.Reason}
	h.handleReplay(writer, request.Context(), sessionID, "stop\x00"+sessionID+"\x00"+idempotencyKey, replayBody, func() (any, error) {
		return h.lifecycle.Stop(request.Context(), session.StopRealtimeCommand{
			SessionID: sessionID, TraceID: body.TraceID, Reason: body.Reason, EndedAt: body.EndedAt,
		})
	})
}

func validStopReason(reason string) bool {
	switch reason {
	case "user_requested", "operator_cancelled", "client_disconnected":
		return true
	default:
		return false
	}
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

func (h *Handler) connection(writer http.ResponseWriter, request *http.Request) {
	sessionID := request.PathValue("session_id")
	if _, err := h.authorize(request.Context(), request, sessionID); err != nil {
		h.writeError(writer, request, err)
		return
	}
	snapshot, err := h.connections.GetCurrent(request.Context(), sessionID)
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

func (h *Handler) handleReplay(writer http.ResponseWriter, ctx context.Context, sessionID, key string, body any, operation func() (any, error)) {
	h.handleReplayStatus(writer, ctx, sessionID, key, body, http.StatusOK, operation, nil)
}

func (h *Handler) handleReplayStatus(writer http.ResponseWriter, ctx context.Context, sessionID, key string, body any, status int, operation func() (any, error), transform func(any, bool) any) {
	hash, err := bodyHash(body)
	if err != nil {
		h.writeError(writer, nil, err)
		return
	}
	record, owner, err := h.reserveReplay(sessionID, key, hash)
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
			value := record.value
			if transform != nil {
				value = transform(value, true)
			}
			h.writeJSON(writer, status, value)
		case <-ctx.Done():
			h.writeError(writer, nil, ctx.Err())
		}
		return
	}

	value, err := operation()
	h.replayMu.Lock()
	if err != nil {
		h.removeReplayLocked(key, record)
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
	if transform != nil {
		value = transform(value, false)
	}
	h.writeJSON(writer, status, value)
}

func (h *Handler) reserveReplay(sessionID, key, hash string) (*replayRecord, bool, error) {
	now := h.now()
	h.replayMu.Lock()
	defer h.replayMu.Unlock()
	if previous, ok := h.replays[key]; ok {
		if !previous.expiresAt.IsZero() && !previous.expiresAt.After(now) {
			h.removeReplayLocked(key, previous)
		} else {
			if previous.hash != hash {
				return nil, false, webrtc.ErrIdempotencyPayloadConflict
			}
			return previous, false, nil
		}
	}
	h.purgeExpiredReplaysLocked(now)
	if len(h.replays) >= h.replayMaxEntries || h.replayEntriesBySession[sessionID] >= h.replayMaxEntriesPerSession {
		return nil, false, ErrReplayCapacity
	}
	record := &replayRecord{sessionID: sessionID, hash: hash, ready: make(chan struct{})}
	h.replays[key] = record
	h.replayEntriesBySession[sessionID]++
	return record, true, nil
}

func (h *Handler) purgeExpiredReplaysLocked(now time.Time) {
	for key, record := range h.replays {
		if record.expiresAt.IsZero() || record.expiresAt.After(now) {
			continue
		}
		h.removeReplayLocked(key, record)
	}
}

func (h *Handler) removeReplayLocked(key string, expected *replayRecord) {
	record, ok := h.replays[key]
	if !ok || (expected != nil && record != expected) {
		return
	}
	delete(h.replays, key)
	if count := h.replayEntriesBySession[record.sessionID] - 1; count > 0 {
		h.replayEntriesBySession[record.sessionID] = count
	} else {
		delete(h.replayEntriesBySession, record.sessionID)
	}
}

func requiredIdempotencyKey(request *http.Request) (string, error) {
	raw := request.Header.Get("Idempotency-Key")
	if len([]byte(raw)) > maxIdempotencyKeyBytes {
		return "", ErrIdempotencyKeyTooLong
	}
	key := strings.TrimSpace(raw)
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
	body, err := io.ReadAll(io.LimitReader(request.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
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
		errors.Is(err, session.ErrStartOperationIDRequired),
		errors.Is(err, webrtc.ErrOfferSDPRequired), errors.Is(err, webrtc.ErrOfferTypeInvalid),
		errors.Is(err, webrtc.ErrIdempotencyKeyRequired), errors.Is(err, ErrIdempotencyKeyTooLong),
		errors.Is(err, webrtc.ErrConnectionIDRequired), errors.Is(err, webrtc.ErrCandidateIDRequired),
		errors.Is(err, webrtc.ErrCandidateRequired):
		return http.StatusBadRequest, "invalid_request"
	case errors.Is(err, webrtc.ErrTTSCodecUnsupported):
		return http.StatusBadRequest, "tts_codec_unsupported"
	case errors.Is(err, ErrTicketRequired), errors.Is(err, webrtc.ErrRealtimeTokenRequired),
		errors.Is(err, webrtc.ErrTicketExpired), errors.Is(err, webrtc.ErrTicketSessionMismatch),
		errors.Is(err, webrtc.ErrTicketAccountRequired):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, session.ErrRuntimeNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, session.ErrInvalidRuntimeTransition):
		return http.StatusConflict, "conflict"
	case errors.Is(err, webrtc.ErrConnectionNotFound):
		return http.StatusNotFound, string(realtimev1.ErrorConnectionNotFound)
	case errors.Is(err, session.ErrRuntimeOperationConflict):
		return http.StatusConflict, string(realtimev1.ErrorRuntimeOperationConflict)
	case errors.Is(err, session.ErrRuntimeCleanupRequired), errors.Is(err, session.ErrSessionNotCreated),
		errors.Is(err, webrtc.ErrIdempotencyPayloadConflict), errors.Is(err, webrtc.ErrConnectionAlreadyExists),
		errors.Is(err, webrtc.ErrConnectionClosing), errors.Is(err, webrtc.ErrCandidatesCompleted),
		errors.Is(err, ErrConfigSession):
		return http.StatusConflict, "conflict"
	case errors.Is(err, ErrReplayCapacity):
		return http.StatusServiceUnavailable, "replay_capacity_exhausted"
	case errors.Is(err, ErrFallbackPlaybackInProgress):
		return http.StatusConflict, "fallback_playback_in_progress"
	case errors.Is(err, ErrInvalidDependency):
		return http.StatusNotImplemented, "not_implemented"
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
var _ ConnectionReader = (webrtc.ConnectionManager)(nil)
