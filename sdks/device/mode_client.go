package device

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

var (
	ErrInvalidConfig       = errors.New("device client configuration is invalid")
	ErrInvalidRequest      = errors.New("device mode request is invalid")
	ErrInvalidResponse     = errors.New("device mode response is invalid")
	ErrUnauthorized        = errors.New("device realtime ticket was rejected")
	ErrGenerationConflict  = errors.New("device mode generation conflict")
	ErrRuntimeInstanceGone = errors.New("device runtime instance changed")
	ErrModeNotAvailable    = errors.New("device mode is not available")
	ErrOperationConflict   = errors.New("device mode operation conflicts")
	ErrOperationDiscarded  = errors.New("device mode operation was discarded")

	// Contract-named aliases make conflict handling easy to share with clients
	// that already use the realtime error vocabulary.
	ErrModeGenerationConflict      = ErrGenerationConflict
	ErrModeRuntimeInstanceMismatch = ErrRuntimeInstanceGone
)

// TicketSource isolates device credential refresh from the mode transport.
// Implementations may cache and refresh short-lived API-issued tickets.
type TicketSource interface {
	Ticket(context.Context, string) (string, error)
}

// TicketSourceFunc adapts a function to TicketSource.
type TicketSourceFunc func(context.Context, string) (string, error)

func (f TicketSourceFunc) Ticket(ctx context.Context, sessionID string) (string, error) {
	if f == nil {
		return "", ErrInvalidConfig
	}
	return f(ctx, sessionID)
}

// ModeTransport is the smallest transport needed by ModeController.
type ModeTransport interface {
	GetModeState(context.Context, string) (ModeStateSnapshot, error)
	SwitchMode(context.Context, SwitchModeCommand) (SwitchModeResult, error)
}

// HTTPModeTransport calls the realtime mode control-plane endpoints. It does
// not create a PeerConnection and therefore remains independently removable.
type HTTPModeTransport struct {
	BaseURL     string
	HTTPClient  *http.Client
	Ticket      TicketSource
	MaxResponse int64
}

const defaultMaxResponse = 64 * 1024

func (c *HTTPModeTransport) GetModeState(ctx context.Context, sessionID string) (ModeStateSnapshot, error) {
	if err := c.validate(sessionID); err != nil {
		return ModeStateSnapshot{}, err
	}
	token, err := c.Ticket.Ticket(ctx, sessionID)
	if err != nil {
		return ModeStateSnapshot{}, err
	}
	if strings.TrimSpace(token) == "" {
		return ModeStateSnapshot{}, ErrUnauthorized
	}
	response, err := c.do(ctx, http.MethodGet, sessionID, token, "", nil)
	if err != nil {
		return ModeStateSnapshot{}, err
	}
	defer response.Body.Close()
	var state ModeStateSnapshot
	if err := decodeBody(response.Body, &state, c.maxResponse()); err != nil {
		return ModeStateSnapshot{}, fmt.Errorf("%w: mode state: %v", ErrInvalidResponse, err)
	}
	if !validModeState(state, sessionID) {
		return ModeStateSnapshot{}, ErrInvalidResponse
	}
	return state, nil
}

func (c *HTTPModeTransport) SwitchMode(ctx context.Context, command SwitchModeCommand) (SwitchModeResult, error) {
	if strings.TrimSpace(command.SessionID) == "" || strings.TrimSpace(command.OperationID) == "" ||
		strings.TrimSpace(command.RuntimeInstanceID) == "" || strings.TrimSpace(command.TraceID) == "" ||
		command.ExpectedGeneration < 1 || !command.TargetMode.Valid() {
		return SwitchModeResult{}, ErrInvalidRequest
	}
	if err := c.validate(command.SessionID); err != nil {
		return SwitchModeResult{}, err
	}
	token, err := c.Ticket.Ticket(ctx, command.SessionID)
	if err != nil {
		return SwitchModeResult{}, err
	}
	if strings.TrimSpace(token) == "" {
		return SwitchModeResult{}, ErrUnauthorized
	}
	body, err := json.Marshal(command)
	if err != nil {
		return SwitchModeResult{}, fmt.Errorf("%w: encode command: %v", ErrInvalidRequest, err)
	}
	response, err := c.do(ctx, http.MethodPost, command.SessionID, token, "mode:"+command.OperationID, bytes.NewReader(body))
	if err != nil {
		return SwitchModeResult{}, err
	}
	defer response.Body.Close()
	var result SwitchModeResult
	if err := decodeBody(response.Body, &result, c.maxResponse()); err != nil {
		return SwitchModeResult{}, fmt.Errorf("%w: mode result: %v", ErrInvalidResponse, err)
	}
	if !validModeResult(result, command) {
		return SwitchModeResult{}, ErrInvalidResponse
	}
	return result, nil
}

func (c *HTTPModeTransport) validate(sessionID string) error {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(sessionID) == "" || c.Ticket == nil {
		return ErrInvalidConfig
	}
	if _, err := url.ParseRequestURI(c.BaseURL); err != nil {
		return fmt.Errorf("%w: base URL: %v", ErrInvalidConfig, err)
	}
	return nil
}

func (c *HTTPModeTransport) do(ctx context.Context, method, sessionID, token, idempotencyKey string, body io.Reader) (*http.Response, error) {
	endpoint, err := url.JoinPath(strings.TrimRight(c.BaseURL, "/"), "realtime/v1/sessions", sessionID, "mode")
	if err != nil {
		return nil, fmt.Errorf("%w: mode endpoint: %v", ErrInvalidConfig, err)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("%w: mode request: %v", ErrInvalidRequest, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer response.Body.Close()
		return nil, decodeHTTPError(response.StatusCode, response.Body, c.maxResponse())
	}
	return response, nil
}

func (c *HTTPModeTransport) maxResponse() int64 {
	if c.MaxResponse > 0 {
		return c.MaxResponse
	}
	return defaultMaxResponse
}

type httpErrorBody struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

type HTTPError struct {
	Status int
	Code   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("realtime mode HTTP %d: %s", e.Status, e.Code) }

func (e *HTTPError) Is(target error) bool {
	switch e.Code {
	case "unauthorized":
		return target == ErrUnauthorized
	case "mode_generation_conflict":
		return target == ErrGenerationConflict
	case "mode_runtime_instance_mismatch":
		return target == ErrRuntimeInstanceGone
	case "mode_not_available":
		return target == ErrModeNotAvailable
	case "mode_operation_conflict":
		return target == ErrOperationConflict
	default:
		return false
	}
}

func decodeHTTPError(status int, body io.Reader, limit int64) error {
	var payload httpErrorBody
	if err := decodeBody(body, &payload, limit); err != nil {
		return &HTTPError{Status: status, Code: "unknown"}
	}
	code := payload.Error.Code
	if code == "" {
		code = "unknown"
	}
	return &HTTPError{Status: status, Code: code}
}

func decodeBody(reader io.Reader, value any, limit int64) error {
	decoder := json.NewDecoder(io.LimitReader(reader, limit+1))
	if err := decoder.Decode(value); err != nil {
		return err
	}
	return nil
}

func validModeState(state ModeStateSnapshot, sessionID string) bool {
	return state.SessionID == sessionID && strings.TrimSpace(state.RuntimeInstanceID) != "" &&
		state.ActiveMode.Valid() && state.Generation >= 1 && state.Phase.Valid() && !state.UpdatedAt.IsZero()
}

func validModeResult(result SwitchModeResult, command SwitchModeCommand) bool {
	if result.OperationID != command.OperationID || !result.Status.Valid() ||
		!validModeState(result.State, command.SessionID) || result.State.RuntimeInstanceID != command.RuntimeInstanceID ||
		result.State.ActiveMode != command.TargetMode || result.State.Phase != ModePhaseActive ||
		result.State.LastOperationID == nil || *result.State.LastOperationID != command.OperationID {
		return false
	}
	if result.Status == ModeSwitchApplied {
		return result.State.Generation == command.ExpectedGeneration+1
	}
	return result.Status == ModeSwitchUnchanged && result.State.Generation == command.ExpectedGeneration
}

// ModeController owns only a client-side observed snapshot. Realtime remains
// authoritative; a conflict refreshes the snapshot and invalidates the old op.
type ModeController struct {
	transport ModeTransport
	sessionID string

	mu        sync.Mutex
	state     ModeStateSnapshot
	hasState  bool
	discarded map[string]struct{}
	retired   map[string]struct{}
}

func NewModeController(transport ModeTransport, sessionID string) (*ModeController, error) {
	if transport == nil || strings.TrimSpace(sessionID) == "" {
		return nil, ErrInvalidConfig
	}
	return &ModeController{transport: transport, sessionID: sessionID, discarded: make(map[string]struct{}), retired: make(map[string]struct{})}, nil
}

func (c *ModeController) Refresh(ctx context.Context) (ModeStateSnapshot, error) {
	state, err := c.transport.GetModeState(ctx, c.sessionID)
	if err != nil {
		return ModeStateSnapshot{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.applyModeLocked(state) {
		return c.state, nil
	}
	return state, nil
}

func (c *ModeController) Snapshot() (ModeStateSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state, c.hasState
}

func (c *ModeController) Switch(ctx context.Context, target Mode) (SwitchModeResult, error) {
	c.mu.Lock()
	ok := c.hasState
	c.mu.Unlock()
	if !ok {
		return SwitchModeResult{}, fmt.Errorf("%w: refresh mode before switching", ErrInvalidRequest)
	}
	operationID, err := newID("mode")
	if err != nil {
		return SwitchModeResult{}, err
	}
	return c.SwitchOperation(ctx, operationID, operationID, target)
}

func (c *ModeController) SwitchOperation(ctx context.Context, operationID, traceID string, target Mode) (SwitchModeResult, error) {
	if strings.TrimSpace(operationID) == "" || strings.TrimSpace(traceID) == "" || !target.Valid() {
		return SwitchModeResult{}, ErrInvalidRequest
	}
	c.mu.Lock()
	if _, discarded := c.discarded[operationID]; discarded {
		c.mu.Unlock()
		return SwitchModeResult{}, ErrOperationDiscarded
	}
	state, ok := c.state, c.hasState
	c.mu.Unlock()
	if !ok {
		return SwitchModeResult{}, fmt.Errorf("%w: refresh mode before switching", ErrInvalidRequest)
	}
	command := SwitchModeCommand{SessionID: c.sessionID, RuntimeInstanceID: state.RuntimeInstanceID,
		OperationID: operationID, TraceID: traceID, ExpectedGeneration: state.Generation, TargetMode: target}
	result, err := c.transport.SwitchMode(ctx, command)
	if err == nil {
		c.mu.Lock()
		accepted := c.applyModeLocked(result.State)
		current := c.state
		responseIsCurrent := current.RuntimeInstanceID == result.State.RuntimeInstanceID &&
			current.Generation == result.State.Generation && current.ActiveMode == result.State.ActiveMode
		if !accepted && !responseIsCurrent {
			c.discarded[operationID] = struct{}{}
			c.mu.Unlock()
			return SwitchModeResult{}, ErrOperationDiscarded
		}
		c.mu.Unlock()
		return result, nil
	}
	if errors.Is(err, ErrGenerationConflict) || errors.Is(err, ErrRuntimeInstanceGone) {
		c.mu.Lock()
		c.discarded[operationID] = struct{}{}
		c.mu.Unlock()
		_, refreshErr := c.Refresh(ctx)
		if refreshErr != nil {
			return SwitchModeResult{}, errors.Join(err, refreshErr, ErrOperationDiscarded)
		}
		return SwitchModeResult{}, errors.Join(err, ErrOperationDiscarded)
	}
	return SwitchModeResult{}, err
}

func (c *ModeController) applyModeLocked(next ModeStateSnapshot) bool {
	if !c.hasState {
		c.state, c.hasState = next, true
		return true
	}
	if c.state.RuntimeInstanceID != next.RuntimeInstanceID {
		if _, retired := c.retired[next.RuntimeInstanceID]; retired || !next.UpdatedAt.After(c.state.UpdatedAt) {
			return false
		}
		c.retired[c.state.RuntimeInstanceID] = struct{}{}
	} else if !newerModeState(c.state, next) {
		return false
	}
	c.state = next
	return true
}

func newerModeState(previous, next ModeStateSnapshot) bool {
	if previous.RuntimeInstanceID != next.RuntimeInstanceID {
		return next.UpdatedAt.After(previous.UpdatedAt)
	}
	return next.Generation > previous.Generation ||
		(next.Generation == previous.Generation && next.UpdatedAt.After(previous.UpdatedAt))
}

func newID(prefix string) (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate %s operation ID: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(raw), nil
}
