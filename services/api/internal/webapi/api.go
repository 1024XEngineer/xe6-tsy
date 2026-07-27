package webapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
)

// API adapts versioned HTTP requests to account, usage, and delivery use cases.
type API struct {
	accounts accounts.Service
	usage    usage.Service
	delivery delivery.Service
	tokens   accounts.AccessTokenVerifier
}

// New builds the account/usage/delivery HTTP mux. Callers may register
// additional module routes on the returned ServeMux before serving. Protected
// routes fail closed unless the required token verifier establishes an account.
func New(accountsService accounts.Service, usageService usage.Service, deliveryService delivery.Service, tokens accounts.AccessTokenVerifier) *http.ServeMux {
	a := &API{accounts: accountsService, usage: usageService, delivery: deliveryService, tokens: tokens}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/anonymous", a.createAnonymous)
	mux.HandleFunc("POST /api/v1/auth/verification-codes", a.createPhoneChallenge)
	mux.HandleFunc("POST /api/v1/auth/phone/login", a.verifyPhone)
	mux.HandleFunc("POST /api/v1/auth/token/refresh", a.refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.Handle("GET /api/v1/account/me", a.authenticate(http.HandlerFunc(a.me)))
	mux.Handle("GET /api/v1/voice-sessions/{id}/usage", a.authenticate(http.HandlerFunc(a.sessionUsage)))
	mux.Handle("GET /api/v1/usage/summary", a.authenticate(http.HandlerFunc(a.accountUsage)))
	mux.Handle("POST /api/v1/outbound-messages", a.authenticate(http.HandlerFunc(a.createMessage)))
	mux.Handle("GET /api/v1/outbound-messages/{message_id}", a.authenticate(http.HandlerFunc(a.getMessage)))
	mux.Handle("POST /api/v1/outbound-deliveries/{message_id}/retry", a.authenticate(http.HandlerFunc(a.retryMessage)))
	mux.Handle("GET /api/v1/account/message-preferences", a.authenticate(http.HandlerFunc(a.preferences)))
	mux.Handle("PUT /api/v1/account/message-preferences/{channel}", a.authenticate(http.HandlerFunc(a.putPreference)))
	return mux
}

// authenticate accepts only a verified Bearer token and replaces any preexisting
// account context with the identity returned by the verifier.
func (a *API) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || a.tokens == nil {
			writeError(w, r, domain.ErrUnauthorized)
			return
		}

		claims, err := a.tokens.VerifyAccessToken(r.Context(), parts[1])
		if err != nil || claims.AccountID == "" {
			writeError(w, r, domain.ErrUnauthorized)
			return
		}

		ctx := WithAccountID(r.Context(), claims.AccountID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// errorResponse is the shared public error envelope defined by the OpenAPI contract.
type errorResponse struct {
	Error struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		RequestID string         `json:"request_id"`
		Retryable bool           `json:"retryable"`
		Details   map[string]any `json:"details"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError maps stable domain errors to the shared HTTP error contract.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, domain.ErrNotImplemented):
		status, code = http.StatusNotImplemented, "not_implemented"
	case errors.Is(err, domain.ErrInvalidArgument):
		status, code = http.StatusBadRequest, "invalid_argument"
	case errors.Is(err, domain.ErrUnauthorized):
		status, code = http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, domain.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	}
	var response errorResponse
	response.Error.Code = code
	response.Error.Message = code
	response.Error.RequestID = requestID(r)
	response.Error.Details = map[string]any{}
	writeJSON(w, status, response)
}

// requestID preserves an upstream request identifier or creates a non-sensitive fallback.
func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "req_unavailable"
	}
	return "req_" + hex.EncodeToString(bytes)
}

// decodeJSON accepts one bounded JSON value and rejects unknown fields or trailing content.
func decodeJSON(r *http.Request, target any) error {
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

// accountID reads only authentication middleware output, never client account fields.
func accountID(r *http.Request) (string, error) {
	id, ok := accountIDFromContext(r.Context())
	if !ok {
		return "", domain.ErrUnauthorized
	}
	return id, nil
}

func (a *API) createAnonymous(w http.ResponseWriter, r *http.Request) {
	result, err := a.accounts.CreateAnonymous(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (a *API) createPhoneChallenge(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Phone string `json:"phone"`
	}
	if decodeJSON(r, &request) != nil || request.Phone == "" {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	id, err := a.accounts.CreatePhoneChallenge(r.Context(), request.Phone)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"challenge_id": id})
}

func (a *API) verifyPhone(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ChallengeID        string `json:"challenge_id"`
		Code               string `json:"code"`
		AnonymousAccountID string `json:"anonymous_account_id,omitempty"`
	}
	if decodeJSON(r, &request) != nil || request.ChallengeID == "" || request.Code == "" {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	result, err := a.accounts.VerifyPhone(r.Context(), request.ChallengeID, request.Code, request.AnonymousAccountID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RefreshToken string `json:"refresh_token"`
	}
	if decodeJSON(r, &request) != nil || request.RefreshToken == "" {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	result, err := a.accounts.Refresh(r.Context(), request.RefreshToken)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RefreshToken string `json:"refresh_token"`
	}
	if decodeJSON(r, &request) != nil || request.RefreshToken == "" {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	if err := a.accounts.Logout(r.Context(), request.RefreshToken); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := a.accounts.Me(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) sessionUsage(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := a.usage.SessionUsage(r.Context(), id, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) accountUsage(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	start, e1 := time.Parse(time.RFC3339, r.URL.Query().Get("period_start"))
	end, e2 := time.Parse(time.RFC3339, r.URL.Query().Get("period_end"))
	if e1 != nil || e2 != nil || !start.Before(end) {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	result, err := a.usage.AccountUsage(r.Context(), id, start, end)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) createMessage(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var input delivery.CreateInput
	if decodeJSON(r, &input) != nil || input.Channel != delivery.ChannelEmail || input.DestinationRef == "" || len(input.TurnIDs) == 0 || r.Header.Get("Idempotency-Key") == "" || hasDuplicates(input.TurnIDs) {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	input.AccountID = id
	input.IdempotencyKey = r.Header.Get("Idempotency-Key")
	result, err := a.delivery.Create(r.Context(), input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (a *API) getMessage(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := a.delivery.Get(r.Context(), id, r.PathValue("message_id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) retryMessage(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if r.Header.Get("Idempotency-Key") == "" {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	result, err := a.delivery.Retry(r.Context(), id, r.PathValue("message_id"), r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (a *API) preferences(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result, err := a.delivery.Preferences(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (a *API) putPreference(w http.ResponseWriter, r *http.Request) {
	id, err := accountID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	channel := delivery.Channel(r.PathValue("channel"))
	if channel != delivery.ChannelEmail {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if decodeJSON(r, &request) != nil || request.Enabled == nil {
		writeError(w, r, domain.ErrInvalidArgument)
		return
	}
	result, err := a.delivery.PutPreference(r.Context(), id, channel, *request.Enabled)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// hasDuplicates also rejects empty identifiers so Turn selection stays unambiguous.
func hasDuplicates(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return true
		}
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}
