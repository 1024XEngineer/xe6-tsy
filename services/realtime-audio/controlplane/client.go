package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

const maxClientResponseBytes = 1 << 20

var (
	ErrClientDependency         = errors.New("invalid realtime client dependency")
	ErrClientRequest            = errors.New("invalid realtime client request")
	ErrClientUnauthorized       = errors.New("realtime client unauthorized")
	ErrClientConflict           = errors.New("realtime client conflict")
	ErrRuntimeOperationConflict = errors.New("realtime runtime operation conflict")
	ErrRuntimeNotFound          = errors.New("realtime runtime not found")
	ErrConnectionNotFound       = errors.New("realtime connection not found")
	ErrDependencyUnavailable    = errors.New("realtime dependency unavailable")
	ErrInvalidResponse          = errors.New("invalid realtime response")
)

// TicketSource returns a short-lived bearer credential scoped to one Session.
// The client asks for a fresh value per request and never persists credentials.
type TicketSource interface {
	Token(ctx context.Context, sessionID string) (string, error)
}

// TicketSourceFunc adapts a function to TicketSource.
type TicketSourceFunc func(context.Context, string) (string, error)

func (f TicketSourceFunc) Token(ctx context.Context, sessionID string) (string, error) {
	return f(ctx, sessionID)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type ClientConfig struct {
	BaseURL string
	HTTP    HTTPDoer
	Tickets TicketSource
}

// Client is the typed cross-service boundary for Session lifecycle and WebRTC
// state. It communicates only through /realtime/v1 and never shares provider
// memory or mutation-capable managers with the API service.
type Client struct {
	baseURL string
	http    HTTPDoer
	tickets TicketSource
}

func NewClient(config ClientConfig) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		config.HTTP == nil || config.Tickets == nil {
		return nil, ErrClientDependency
	}
	return &Client{baseURL: baseURL, http: config.HTTP, tickets: config.Tickets}, nil
}

func (c *Client) Start(
	ctx context.Context,
	sessionID string,
	request realtimev1.StartRequest,
) (realtimev1.RuntimeSnapshot, error) {
	if sessionID == "" || request.OperationID == "" {
		return realtimev1.RuntimeSnapshot{}, ErrClientRequest
	}
	var snapshot realtimev1.RuntimeSnapshot
	if err := c.doJSON(
		ctx, http.MethodPost, sessionID, "start",
		"start:"+request.OperationID, request, &snapshot,
	); err != nil {
		return realtimev1.RuntimeSnapshot{}, err
	}
	if err := validateRuntimeSnapshot(snapshot, sessionID); err != nil {
		return realtimev1.RuntimeSnapshot{}, err
	}
	if snapshot.StartOperationID != request.OperationID {
		return realtimev1.RuntimeSnapshot{}, fmt.Errorf(
			"%w: start operation %q does not match %q",
			ErrInvalidResponse, snapshot.StartOperationID, request.OperationID,
		)
	}
	return snapshot, nil
}

func (c *Client) Stop(
	ctx context.Context,
	sessionID string,
	request realtimev1.StopRequest,
) (realtimev1.RuntimeSnapshot, error) {
	if sessionID == "" || request.Reason == "" || request.EndedAt.IsZero() {
		return realtimev1.RuntimeSnapshot{}, ErrClientRequest
	}
	var snapshot realtimev1.RuntimeSnapshot
	if err := c.doJSON(
		ctx, http.MethodPost, sessionID, "stop",
		requestIdempotencyKey("stop", request), request, &snapshot,
	); err != nil {
		return realtimev1.RuntimeSnapshot{}, err
	}
	if err := validateRuntimeSnapshot(snapshot, sessionID); err != nil {
		return realtimev1.RuntimeSnapshot{}, err
	}
	return snapshot, nil
}

func (c *Client) GetRuntimeState(
	ctx context.Context,
	sessionID string,
) (realtimev1.RuntimeSnapshot, error) {
	if sessionID == "" {
		return realtimev1.RuntimeSnapshot{}, ErrClientRequest
	}
	var snapshot realtimev1.RuntimeSnapshot
	if err := c.doJSON(ctx, http.MethodGet, sessionID, "runtime", "", nil, &snapshot); err != nil {
		return realtimev1.RuntimeSnapshot{}, err
	}
	if err := validateRuntimeSnapshot(snapshot, sessionID); err != nil {
		return realtimev1.RuntimeSnapshot{}, err
	}
	return snapshot, nil
}

func (c *Client) GetConnection(
	ctx context.Context,
	sessionID string,
) (realtimev1.ConnectionSnapshot, error) {
	if sessionID == "" {
		return realtimev1.ConnectionSnapshot{}, ErrClientRequest
	}
	var snapshot realtimev1.ConnectionSnapshot
	if err := c.doJSON(ctx, http.MethodGet, sessionID, "connection", "", nil, &snapshot); err != nil {
		return realtimev1.ConnectionSnapshot{}, err
	}
	if snapshot.SessionID != sessionID || snapshot.ConnectionID == "" ||
		!snapshot.State.Valid() || snapshot.UpdatedAt.IsZero() {
		return realtimev1.ConnectionSnapshot{}, ErrInvalidResponse
	}
	return snapshot, nil
}

func (c *Client) doJSON(
	ctx context.Context,
	method string,
	sessionID string,
	action string,
	idempotencyKey string,
	body any,
	target any,
) error {
	token, err := c.tickets.Token(ctx, sessionID)
	if err != nil {
		return preserveContextError(ctx, fmt.Errorf("read realtime ticket: %w", err))
	}
	if strings.TrimSpace(token) == "" {
		return ErrClientUnauthorized
	}

	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%w: encode request: %v", ErrClientRequest, err)
		}
		payload = bytes.NewReader(encoded)
	}
	endpoint, err := url.JoinPath(c.baseURL, "realtime/v1/sessions", sessionID, action)
	if err != nil {
		return fmt.Errorf("%w: build endpoint: %v", ErrClientDependency, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return fmt.Errorf("%w: build request: %v", ErrClientRequest, err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		httpRequest.Header.Set("Idempotency-Key", idempotencyKey)
	}

	response, err := c.http.Do(httpRequest)
	if err != nil {
		return preserveContextError(ctx, fmt.Errorf("%w: %v", ErrDependencyUnavailable, err))
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxClientResponseBytes+1)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeClientError(response.StatusCode, reader)
	}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode response: %v", ErrInvalidResponse, err)
	}
	return nil
}

type errorResponse struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func decodeClientError(status int, reader io.Reader) error {
	var response errorResponse
	_ = json.NewDecoder(reader).Decode(&response)
	switch response.Error.Code {
	case "invalid_request":
		return ErrClientRequest
	case "unauthorized":
		return ErrClientUnauthorized
	case string(realtimev1.ErrorRuntimeNotFound):
		return ErrRuntimeNotFound
	case string(realtimev1.ErrorConnectionNotFound):
		return ErrConnectionNotFound
	case string(realtimev1.ErrorRuntimeOperationConflict):
		return ErrRuntimeOperationConflict
	case "conflict":
		return ErrClientConflict
	}
	if status >= http.StatusInternalServerError {
		return ErrDependencyUnavailable
	}
	return fmt.Errorf("%w: HTTP %d", ErrInvalidResponse, status)
}

func validateRuntimeSnapshot(snapshot realtimev1.RuntimeSnapshot, sessionID string) error {
	if snapshot.SessionID != sessionID || !snapshot.RuntimeState.Valid() ||
		snapshot.UpdatedAt.IsZero() {
		return ErrInvalidResponse
	}
	return nil
}

func preserveContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return err
}

func requestIdempotencyKey(prefix string, request any) string {
	encoded, _ := json.Marshal(request)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%s:%x", prefix, sum)
}

var _ TicketSource = TicketSourceFunc(nil)
