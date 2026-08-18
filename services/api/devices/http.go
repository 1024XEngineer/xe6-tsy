package devices

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	internalwebapi "github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
)

type deviceContextKey struct{}

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// Register mounts pairing and challenge endpoints. Provisioning is deliberately
// absent from the public HTTP API: manufacturing creates device records through
// the trusted service boundary before shipment.
func (h *Handler) Register(mux *http.ServeMux, accountAuth func(http.Handler) http.Handler, deviceAuth func(http.Handler) http.Handler) {
	if h == nil || h.service == nil || accountAuth == nil || deviceAuth == nil {
		panic("device HTTP dependencies are required")
	}
	mux.Handle("POST /api/v1/account/device-pairing-codes", accountAuth(http.HandlerFunc(h.createPairingCode)))
	mux.Handle("GET /api/v1/account/devices", accountAuth(http.HandlerFunc(h.listDevices)))
	mux.Handle("DELETE /api/v1/account/devices/{device_id}", accountAuth(http.HandlerFunc(h.revokeDevice)))
	mux.Handle("POST /api/v1/devices/pair", http.HandlerFunc(h.pair))
	mux.Handle("POST /api/v1/device-auth/challenges", http.HandlerFunc(h.createChallenge))
	mux.Handle("POST /api/v1/device-auth/tokens", http.HandlerFunc(h.exchangeChallenge))
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	accountID, ok := internalwebapi.AccountIDFromContext(r.Context())
	if !ok {
		writeError(w, r, domain.ErrUnauthorized)
		return
	}
	items, err := h.service.ListBound(r.Context(), accountID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) revokeDevice(w http.ResponseWriter, r *http.Request) {
	accountID, ok := internalwebapi.AccountIDFromContext(r.Context())
	if !ok {
		writeError(w, r, domain.ErrUnauthorized)
		return
	}
	if err := h.service.Revoke(r.Context(), accountID, r.PathValue("device_id")); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) RegisterSessions(mux *http.ServeMux, sessionHandler *sessions.Handler, deviceAuth func(http.Handler) http.Handler) {
	if h == nil || h.service == nil || sessionHandler == nil || deviceAuth == nil {
		panic("device session dependencies are required")
	}
	sessionHandler.RegisterDevice(mux, deviceAuth, sessions.DeviceSessionAccess{
		DeviceID: DeviceIDFromRequest,
		Owns:     h.service.OwnsSession,
	})
}

func (h *Handler) createPairingCode(w http.ResponseWriter, r *http.Request) {
	if err := rejectNonEmptyBody(r); err != nil {
		writeError(w, r, err)
		return
	}
	accountID, ok := internalwebapi.AccountIDFromContext(r.Context())
	if !ok {
		writeError(w, r, domain.ErrUnauthorized)
		return
	}
	code, expiresAt, err := h.service.CreatePairingCode(r.Context(), accountID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"pairing_code": code, "expires_at": expiresAt})
}

func (h *Handler) pair(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DeviceID    string `json:"device_id"`
		PairingCode string `json:"pairing_code"`
		Signature   string `json:"signature"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	signature, err := decodeBase64(request.Signature)
	if err != nil {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	device, err := h.service.Pair(r.Context(), request.DeviceID, request.PairingCode, signature)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (h *Handler) createChallenge(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DeviceID string `json:"device_id"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	challenge, err := h.service.CreateChallenge(r.Context(), request.DeviceID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"challenge_id": challenge.ID, "nonce": challenge.Nonce, "expires_at": challenge.ExpiresAt})
}

func (h *Handler) exchangeChallenge(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DeviceID    string `json:"device_id"`
		ChallengeID string `json:"challenge_id"`
		Signature   string `json:"signature"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	signature, err := decodeBase64(request.Signature)
	if err != nil {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	token, err := h.service.ExchangeChallenge(r.Context(), request.DeviceID, request.ChallengeID, signature)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, token)
}

// Authenticate validates only a device token and writes its bound account and
// concrete device identity into the request context. It never accepts user
// access tokens, preventing accidental credential interchangeability.
func (h *Handler) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if next == nil || h == nil || h.service == nil {
			writeError(w, r, domain.ErrUnauthorized)
			return
		}
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeError(w, r, domain.ErrUnauthorized)
			return
		}
		claims, err := h.service.Verify(r.Context(), token)
		if err != nil {
			writeError(w, r, domain.ErrUnauthorized)
			return
		}
		ctx := internalwebapi.WithAccountID(r.Context(), claims.AccountID)
		ctx = context.WithValue(ctx, deviceContextKey{}, claims.DeviceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func DeviceIDFromRequest(r *http.Request) (string, bool) {
	value, ok := r.Context().Value(deviceContextKey{}).(string)
	return value, ok && value != ""
}

func bearerToken(value string) (string, bool) {
	parts := strings.Fields(value)
	returnToken := ""
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] != "" {
		returnToken = parts[1]
	}
	return returnToken, returnToken != ""
}
func decodeBase64(value string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(value) }

func decodeJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return domain.ErrInvalidArgument
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return domain.ErrInvalidArgument
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return domain.ErrInvalidArgument
	}
	return nil
}

func rejectNonEmptyBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2))
	if err != nil || len(strings.TrimSpace(string(body))) != 0 {
		return domain.ErrInvalidArgument
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, domain.ErrInvalidArgument):
		status, code = http.StatusBadRequest, "invalid_argument"
	case errors.Is(err, domain.ErrUnauthorized):
		status, code = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, domain.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	}
	requestID := "req_device"
	if r != nil && strings.TrimSpace(r.Header.Get("X-Request-ID")) != "" {
		requestID = r.Header.Get("X-Request-ID")
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": code, "request_id": requestID, "retryable": false, "details": map[string]any{}}})
}
