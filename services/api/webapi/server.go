// Package webapi exposes the public voice-record HTTP contract over domain services.
package webapi

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/authcontext"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	internalwebapi "github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
	"github.com/1024XEngineer/xe6-tsy/services/api/participants"
	"github.com/1024XEngineer/xe6-tsy/services/api/turns"
)

const systemTokenHeader = "X-Lingow-System-Token"

type systemActorContextKey struct{}

// WithAccountID attaches an account identity established by trusted authentication middleware.
// HTTP handlers never derive the account ID from request headers, query values, or JSON bodies.
func WithAccountID(ctx context.Context, accountID string) context.Context {
	return authcontext.WithAccountID(ctx, accountID)
}

// WithSystemActor marks an internally authenticated request as allowed to correct attribution.
// Only trusted middleware or internal callers may add this marker to a request context.
func WithSystemActor(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemActorContextKey{}, true)
}

// AccountProvider returns the account authenticated for a request.
type AccountProvider interface {
	AccountID(ctx context.Context) (string, bool)
}

// SystemAuthorizer decides whether a request is an internal system operation.
type SystemAuthorizer interface {
	IsSystem(ctx context.Context) bool
}

// ContextAccountProvider is the bridge for future authentication middleware that uses WithAccountID.
type ContextAccountProvider struct{}

func (ContextAccountProvider) AccountID(ctx context.Context) (string, bool) {
	return authcontext.AccountID(ctx)
}

// ContextSystemAuthorizer is the bridge for future system authentication middleware.
type ContextSystemAuthorizer struct{}

func (ContextSystemAuthorizer) IsSystem(ctx context.Context) bool {
	isSystem, _ := ctx.Value(systemActorContextKey{}).(bool)
	return isSystem
}

// Dependencies are the required domain services and trusted request-boundary dependencies.
type Dependencies struct {
	Participants *participants.Service
	Turns        *turns.Service
	Accounts     AccountProvider
	System       SystemAuthorizer
	SystemToken  string
	Logger       *slog.Logger
}

// Server owns the HTTP adapter. It deliberately has no storage or authentication implementation.
type Server struct {
	participants *participants.Service
	turns        *turns.Service
	accounts     AccountProvider
	system       SystemAuthorizer
	systemToken  string
	logger       *slog.Logger
	requestSeq   atomic.Uint64
}

func NewHandler(dependencies Dependencies) *Server {
	if dependencies.Participants == nil {
		panic("webapi participants service is required")
	}
	if dependencies.Turns == nil {
		panic("webapi turns service is required")
	}
	if dependencies.Accounts == nil {
		panic("webapi account provider is required")
	}
	if dependencies.System == nil {
		panic("webapi system authorizer is required")
	}
	if dependencies.Logger == nil {
		panic("webapi logger is required")
	}

	server := &Server{
		participants: dependencies.Participants,
		turns:        dependencies.Turns,
		accounts:     dependencies.Accounts,
		system:       dependencies.System,
		systemToken:  dependencies.SystemToken,
		logger:       dependencies.Logger,
	}
	return server
}

// RouteMiddleware carries the account and system authentication boundaries applied by Register.
// Read routes require only the account middleware; PATCH routes require both so an internal
// system credential can never substitute for a valid account or vice versa.
type RouteMiddleware struct {
	Account func(http.Handler) http.Handler
	System  func(http.Handler) http.Handler
}

// Register attaches all voice-record routes behind the caller's authentication boundaries.
func (s *Server) Register(mux *http.ServeMux, middleware RouteMiddleware) {
	if middleware.Account == nil {
		panic("webapi account authentication middleware is required")
	}
	if middleware.System == nil {
		panic("webapi system authentication middleware is required")
	}
	patch := func(next http.Handler) http.Handler {
		return middleware.Account(middleware.System(next))
	}
	mux.Handle("GET /api/v1/voice-sessions/{id}/participants", middleware.Account(http.HandlerFunc(s.listParticipants)))
	mux.Handle("PATCH /api/v1/voice-sessions/{id}/participants/{participant_id}", patch(http.HandlerFunc(s.updateParticipant)))
	mux.Handle("GET /api/v1/voice-sessions/{id}/turns", middleware.Account(http.HandlerFunc(s.listSessionTurns)))
	mux.Handle("GET /api/v1/voice-turns/{id}", middleware.Account(http.HandlerFunc(s.getTurn)))
	mux.Handle("PATCH /api/v1/voice-turns/{id}/attribution", patch(http.HandlerFunc(s.correctAttribution)))
	mux.Handle("GET /api/v1/translation-history", middleware.Account(http.HandlerFunc(s.listHistory)))
}

// Authenticate applies the shared Bearer-token parser while preserving the
// voice-record error contract for authentication failures.
func (s *Server) Authenticate(tokens accounts.AccessTokenVerifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, err := internalwebapi.AuthenticatedContext(
			request.Context(),
			request.Header.Get("Authorization"),
			tokens,
		)
		if err != nil {
			s.writeError(writer, recordsv1.ErrorUnauthenticated, errors.New("authentication is required"))
			return
		}
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// SystemAuthenticate validates the X-Lingow-System-Token internal credential and marks the
// request as a system actor. It is fail-closed: without a configured token, or when the header
// is missing or does not match, the request is rejected with forbidden. The comparison is
// constant-time and the configured token is never written to logs or error responses.
func (s *Server) SystemAuthenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if s.systemToken == "" {
			s.writeError(writer, recordsv1.ErrorForbidden, errors.New("system authentication is not configured"))
			return
		}
		provided := request.Header.Get(systemTokenHeader)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(s.systemToken)) != 1 {
			s.writeError(writer, recordsv1.ErrorForbidden, errors.New("system authentication is required"))
			return
		}
		next.ServeHTTP(writer, request.WithContext(WithSystemActor(request.Context())))
	})
}

func (s *Server) listParticipants(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := s.requireAccount(writer, request)
	if !ok {
		return
	}
	query, err := parseParticipantsQuery(request)
	if err != nil {
		s.writeError(writer, recordsv1.ErrorInvalidRequest, err)
		return
	}
	response, err := s.participants.List(request.Context(), accountID, request.PathValue("id"), query)
	if err != nil {
		s.writeDomainError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, response)
}

func (s *Server) updateParticipant(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := s.requireAccount(writer, request)
	if !ok {
		return
	}
	if !s.system.IsSystem(request.Context()) {
		s.writeError(writer, recordsv1.ErrorForbidden, errors.New("system authorization is required"))
		return
	}
	body, err := decodeParticipantUpdate(request.Body)
	if err != nil {
		s.writeError(writer, recordsv1.ErrorInvalidRequest, err)
		return
	}
	participant, err := s.participants.Update(request.Context(), accountID, request.PathValue("id"), request.PathValue("participant_id"), body)
	if err != nil {
		s.writeDomainError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, participant)
}

func (s *Server) listSessionTurns(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := s.requireAccount(writer, request)
	if !ok {
		return
	}
	query, err := parseSessionTurnsQuery(request)
	if err != nil {
		s.writeError(writer, recordsv1.ErrorInvalidRequest, err)
		return
	}
	response, err := s.turns.ListSession(request.Context(), accountID, request.PathValue("id"), query)
	if err != nil {
		s.writeDomainError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, response)
}

func (s *Server) getTurn(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := s.requireAccount(writer, request)
	if !ok {
		return
	}
	turn, err := s.turns.Get(request.Context(), accountID, request.PathValue("id"))
	if err != nil {
		s.writeDomainError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, turn)
}

func (s *Server) correctAttribution(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := s.requireAccount(writer, request)
	if !ok {
		return
	}
	if !s.system.IsSystem(request.Context()) {
		s.writeError(writer, recordsv1.ErrorForbidden, errors.New("system authorization is required"))
		return
	}
	var body attributionUpdateBody
	if err := decodeJSON(request.Body, &body); err != nil {
		s.writeError(writer, recordsv1.ErrorInvalidRequest, err)
		return
	}
	turn, err := s.turns.CorrectAttribution(request.Context(), accountID, request.PathValue("id"), recordsv1.UpdateAttributionRequest{
		ParticipantID:     body.ParticipantID,
		AttributionStatus: body.AttributionStatus,
		SpeakerConfidence: body.SpeakerConfidence.Value,
	}, body.SpeakerConfidence.Set)
	if err != nil {
		s.writeDomainError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, turn)
}

func (s *Server) listHistory(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := s.requireAccount(writer, request)
	if !ok {
		return
	}
	query, err := parseHistoryQuery(request)
	if err != nil {
		s.writeError(writer, recordsv1.ErrorInvalidRequest, err)
		return
	}
	response, err := s.turns.ListHistory(request.Context(), accountID, query)
	if err != nil {
		s.writeDomainError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, response)
}

func (s *Server) requireAccount(writer http.ResponseWriter, request *http.Request) (string, bool) {
	accountID, ok := s.accounts.AccountID(request.Context())
	if !ok {
		s.writeError(writer, recordsv1.ErrorUnauthenticated, errors.New("authentication is required"))
		return "", false
	}
	return accountID, true
}

func (s *Server) writeDomainError(writer http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, errNotImplemented):
		s.writeError(writer, recordsv1.ErrorNotImplemented, err)
	case errors.Is(err, participants.ErrSessionNotFound), errors.Is(err, turns.ErrSessionNotFound):
		s.writeError(writer, recordsv1.ErrorVoiceSessionAbsent, err)
	case errors.Is(err, participants.ErrParticipantNotFound), errors.Is(err, turns.ErrParticipantNotFound):
		s.writeError(writer, recordsv1.ErrorParticipantAbsent, err)
	case errors.Is(err, turns.ErrTurnNotFound):
		s.writeError(writer, recordsv1.ErrorVoiceTurnAbsent, err)
	case errors.Is(err, participants.ErrForbidden), errors.Is(err, turns.ErrForbidden):
		s.writeError(writer, recordsv1.ErrorForbidden, err)
	case errors.Is(err, participants.ErrConflict):
		s.writeError(writer, recordsv1.ErrorConflict, err)
	case errors.Is(err, turns.ErrInvalidAttribution):
		s.writeError(writer, recordsv1.ErrorInvalidAttribution, err)
	case errors.Is(err, participants.ErrInvalidRequest), errors.Is(err, turns.ErrInvalidRequest):
		s.writeError(writer, recordsv1.ErrorInvalidRequest, err)
	default:
		s.logger.ErrorContext(request.Context(), "voice record request failed", "error", err)
		s.writeError(writer, recordsv1.ErrorInternal, errors.New("internal server error"))
	}
}

func (s *Server) writeError(writer http.ResponseWriter, code recordsv1.ErrorCode, cause error) {
	status := http.StatusInternalServerError
	switch code {
	case recordsv1.ErrorInvalidRequest, recordsv1.ErrorInvalidAttribution:
		status = http.StatusBadRequest
	case recordsv1.ErrorUnauthenticated:
		status = http.StatusUnauthorized
	case recordsv1.ErrorForbidden:
		status = http.StatusForbidden
	case recordsv1.ErrorConflict:
		status = http.StatusConflict
	case recordsv1.ErrorVoiceSessionAbsent, recordsv1.ErrorParticipantAbsent, recordsv1.ErrorVoiceTurnAbsent:
		status = http.StatusNotFound
	case recordsv1.ErrorNotImplemented:
		status = http.StatusNotImplemented
	}
	s.writeJSON(writer, status, recordsv1.ErrorResponse{Error: recordsv1.APIError{
		Code:      code,
		Message:   cause.Error(),
		RequestID: fmt.Sprintf("req_%d", s.requestSeq.Add(1)),
	}})
}

func (s *Server) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

type optionalString struct {
	Set   bool
	Value *string
}

func (value *optionalString) UnmarshalJSON(data []byte) error {
	value.Set = true
	var decoded *string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = decoded
	return nil
}

type optionalFloat struct {
	Set   bool
	Value *float64
}

func (value *optionalFloat) UnmarshalJSON(data []byte) error {
	value.Set = true
	var decoded *float64
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	value.Value = decoded
	return nil
}

type participantUpdateBody struct {
	DisplayName       optionalString `json:"display_name"`
	ProviderSpeakerID optionalString `json:"provider_speaker_id"`
	VoiceProfileID    optionalString `json:"voice_profile_id"`
}

type attributionUpdateBody struct {
	ParticipantID     string                      `json:"participant_id"`
	AttributionStatus recordsv1.AttributionStatus `json:"attribution_status"`
	SpeakerConfidence optionalFloat               `json:"speaker_confidence"`
}

func decodeParticipantUpdate(body io.Reader) (participants.Update, error) {
	var decoded participantUpdateBody
	if err := decodeJSON(body, &decoded); err != nil {
		return participants.Update{}, err
	}
	return participants.Update{
		DisplayName:          decoded.DisplayName.Value,
		DisplayNameSet:       decoded.DisplayName.Set,
		ProviderSpeakerID:    decoded.ProviderSpeakerID.Value,
		ProviderSpeakerIDSet: decoded.ProviderSpeakerID.Set,
		VoiceProfileID:       decoded.VoiceProfileID.Value,
		VoiceProfileIDSet:    decoded.VoiceProfileID.Set,
	}, nil
}

// maxRequestBodyBytes bounds request bodies so oversized payloads fail fast instead of being read
// into memory. It matches the limit used by the other public API routes.
const maxRequestBodyBytes = 1 << 20

func decodeJSON(body io.Reader, target any) error {
	// Read up to limit+1 so a request with a complete JSON prefix followed by more data is
	// detected instead of being hidden as an early EOF by io.LimitReader.
	payload, err := io.ReadAll(io.LimitReader(body, maxRequestBodyBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxRequestBodyBytes {
		return errors.New("request body exceeds 1 MiB")
	}

	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}
