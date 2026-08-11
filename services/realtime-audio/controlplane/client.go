package controlplane

import (
	"bytes"
	"context"
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
	ErrClientDependency            = errors.New("invalid realtime client dependency")
	ErrClientRequest               = errors.New("invalid realtime client request")
	ErrClientUnauthorized          = errors.New("realtime client unauthorized")
	ErrClientConflict              = errors.New("realtime client conflict")
	ErrConnectionNotFound          = errors.New("realtime WebRTC connection not found")
	ErrRuntimeNotFound             = errors.New("realtime runtime snapshot not found")
	ErrRuntimeOperationConflict    = errors.New("realtime runtime operation conflict")
	ErrModeNotAvailable            = errors.New("realtime mode is not available")
	ErrModeGenerationConflict      = errors.New("realtime mode generation conflict")
	ErrModeRuntimeInstanceMismatch = errors.New("realtime mode runtime instance mismatch")
	ErrModeOperationConflict       = errors.New("realtime mode operation conflict")
	ErrDependencyUnavailable       = errors.New("realtime dependency unavailable")
	ErrInvalidResponse             = errors.New("invalid realtime response")
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

// Client is the typed cross-service boundary for realtime control-plane calls.
// It communicates only through /realtime/v1 and never shares provider memory
// with the API service.
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

// Start sends the persisted OperationID both in the JSON contract and as the
// transport replay identity. The header never substitutes for the body field.
func (c *Client) Start(
	ctx context.Context,
	sessionID string,
	request realtimev1.StartRequest,
) (realtimev1.RuntimeSnapshot, error) {
	if sessionID == "" || strings.TrimSpace(request.OperationID) == "" {
		return realtimev1.RuntimeSnapshot{}, ErrClientRequest
	}
	return c.postRuntime(
		ctx,
		sessionID,
		"start",
		"start:"+request.OperationID,
		request,
		func(snapshot realtimev1.RuntimeSnapshot) error {
			if snapshot.SessionID != sessionID ||
				!snapshot.RuntimeState.Valid() ||
				snapshot.UpdatedAt.IsZero() ||
				snapshot.StartOperationID != request.OperationID {
				return ErrInvalidResponse
			}
			return nil
		},
	)
}

// Stop confirms that realtime has cleaned up all media resources for one
// business End reason. The replay identity deliberately excludes TraceID and
// EndedAt because End retries may carry fresh audit metadata.
func (c *Client) Stop(
	ctx context.Context,
	sessionID string,
	request realtimev1.StopRequest,
) (realtimev1.RuntimeSnapshot, error) {
	if strings.TrimSpace(sessionID) == "" ||
		strings.TrimSpace(request.Reason) == "" ||
		request.EndedAt.IsZero() {
		return realtimev1.RuntimeSnapshot{}, ErrClientRequest
	}
	return c.postRuntime(
		ctx,
		sessionID,
		"stop",
		"stop:"+request.Reason,
		request,
		func(snapshot realtimev1.RuntimeSnapshot) error {
			if snapshot.SessionID != sessionID ||
				!snapshot.RuntimeState.Valid() ||
				snapshot.UpdatedAt.IsZero() {
				return ErrInvalidResponse
			}
			return nil
		},
	)
}

// PlayFallback submits one immutable translated-text snapshot and accepts a
// repeated operation as an already-accepted receipt.
func (c *Client) PlayFallback(ctx context.Context, sessionID string, request realtimev1.FallbackPlaybackRequest) (realtimev1.FallbackPlaybackReceipt, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(request.OperationID) == "" ||
		request.SessionID != sessionID || strings.TrimSpace(request.TurnID) == "" ||
		strings.TrimSpace(request.TargetLanguage) == "" || strings.TrimSpace(request.TranslatedText) == "" ||
		request.LanguageConfigVersion < 1 || strings.TrimSpace(request.TraceID) == "" {
		return realtimev1.FallbackPlaybackReceipt{}, ErrClientRequest
	}
	token, err := c.ticket(ctx, sessionID)
	if err != nil {
		return realtimev1.FallbackPlaybackReceipt{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return realtimev1.FallbackPlaybackReceipt{}, fmt.Errorf("%w: encode fallback playback request: %v", ErrClientRequest, err)
	}
	endpoint, err := url.JoinPath(c.baseURL, "realtime/v1/sessions", sessionID, "fallback-playback")
	if err != nil {
		return realtimev1.FallbackPlaybackReceipt{}, fmt.Errorf("%w: build fallback playback endpoint: %v", ErrClientDependency, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return realtimev1.FallbackPlaybackReceipt{}, fmt.Errorf("%w: build fallback playback request: %v", ErrClientRequest, err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", "fallback:"+request.OperationID)
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return realtimev1.FallbackPlaybackReceipt{}, preserveContextError(ctx, fmt.Errorf("%w: %v", ErrDependencyUnavailable, err))
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxClientResponseBytes+1)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return realtimev1.FallbackPlaybackReceipt{}, decodeClientError(response.StatusCode, reader)
	}
	var receipt realtimev1.FallbackPlaybackReceipt
	if err := json.NewDecoder(reader).Decode(&receipt); err != nil {
		return realtimev1.FallbackPlaybackReceipt{}, fmt.Errorf("%w: decode fallback playback response: %v", ErrInvalidResponse, err)
	}
	if receipt.OperationID != request.OperationID ||
		(receipt.Status != realtimev1.FallbackPlaybackAccepted && receipt.Status != realtimev1.FallbackPlaybackAlreadyAccepted) {
		return realtimev1.FallbackPlaybackReceipt{}, ErrInvalidResponse
	}
	return receipt, nil
}

// GetRuntimeState reads the authoritative media-plane runtime snapshot.
func (c *Client) GetRuntimeState(
	ctx context.Context,
	sessionID string,
) (realtimev1.RuntimeSnapshot, error) {
	if strings.TrimSpace(sessionID) == "" {
		return realtimev1.RuntimeSnapshot{}, ErrClientRequest
	}
	token, err := c.ticket(ctx, sessionID)
	if err != nil {
		return realtimev1.RuntimeSnapshot{}, err
	}
	endpoint, err := url.JoinPath(
		c.baseURL, "realtime/v1/sessions", sessionID, "runtime",
	)
	if err != nil {
		return realtimev1.RuntimeSnapshot{}, fmt.Errorf(
			"%w: build runtime endpoint: %v", ErrClientDependency, err,
		)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return realtimev1.RuntimeSnapshot{}, fmt.Errorf(
			"%w: build runtime request: %v", ErrClientRequest, err,
		)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(request)
	if err != nil {
		return realtimev1.RuntimeSnapshot{}, preserveContextError(
			ctx, fmt.Errorf("%w: %v", ErrDependencyUnavailable, err),
		)
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxClientResponseBytes+1)
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return realtimev1.RuntimeSnapshot{}, decodeClientError(
			response.StatusCode, reader,
		)
	}
	var snapshot realtimev1.RuntimeSnapshot
	if err := json.NewDecoder(reader).Decode(&snapshot); err != nil {
		return realtimev1.RuntimeSnapshot{}, fmt.Errorf(
			"%w: decode runtime response: %v", ErrInvalidResponse, err,
		)
	}
	if snapshot.SessionID != sessionID ||
		!snapshot.RuntimeState.Valid() ||
		snapshot.UpdatedAt.IsZero() {
		return realtimev1.RuntimeSnapshot{}, ErrInvalidResponse
	}
	return snapshot, nil
}

// GetConnection reads the authoritative WebRTC transport snapshot and rejects
// malformed provider responses before they cross into API session readiness.
func (c *Client) GetConnection(
	ctx context.Context,
	sessionID string,
) (realtimev1.ConnectionSnapshot, error) {
	if strings.TrimSpace(sessionID) == "" {
		return realtimev1.ConnectionSnapshot{}, ErrClientRequest
	}
	token, err := c.ticket(ctx, sessionID)
	if err != nil {
		return realtimev1.ConnectionSnapshot{}, err
	}
	endpoint, err := url.JoinPath(
		c.baseURL, "realtime/v1/sessions", sessionID, "connection",
	)
	if err != nil {
		return realtimev1.ConnectionSnapshot{}, fmt.Errorf(
			"%w: build connection endpoint: %v", ErrClientDependency, err,
		)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return realtimev1.ConnectionSnapshot{}, fmt.Errorf(
			"%w: build connection request: %v", ErrClientRequest, err,
		)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(request)
	if err != nil {
		return realtimev1.ConnectionSnapshot{}, preserveContextError(
			ctx, fmt.Errorf("%w: %v", ErrDependencyUnavailable, err),
		)
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxClientResponseBytes+1)
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return realtimev1.ConnectionSnapshot{}, decodeClientError(
			response.StatusCode, reader,
		)
	}
	var snapshot realtimev1.ConnectionSnapshot
	if err := json.NewDecoder(reader).Decode(&snapshot); err != nil {
		return realtimev1.ConnectionSnapshot{}, fmt.Errorf(
			"%w: decode connection response: %v", ErrInvalidResponse, err,
		)
	}
	if snapshot.SessionID != sessionID ||
		strings.TrimSpace(snapshot.ConnectionID) == "" ||
		!snapshot.State.Valid() || snapshot.Version <= 0 ||
		snapshot.UpdatedAt.IsZero() {
		return realtimev1.ConnectionSnapshot{}, ErrInvalidResponse
	}
	return snapshot, nil
}

func (c *Client) postRuntime(
	ctx context.Context,
	sessionID string,
	action string,
	idempotencyKey string,
	body any,
	validate func(realtimev1.RuntimeSnapshot) error,
) (realtimev1.RuntimeSnapshot, error) {
	token, err := c.ticket(ctx, sessionID)
	if err != nil {
		return realtimev1.RuntimeSnapshot{}, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return realtimev1.RuntimeSnapshot{}, fmt.Errorf(
			"%w: encode %s request: %v", ErrClientRequest, action, err,
		)
	}
	endpoint, err := url.JoinPath(
		c.baseURL, "realtime/v1/sessions", sessionID, action,
	)
	if err != nil {
		return realtimev1.RuntimeSnapshot{}, fmt.Errorf(
			"%w: build %s endpoint: %v", ErrClientDependency, action, err,
		)
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, bytes.NewReader(encoded),
	)
	if err != nil {
		return realtimev1.RuntimeSnapshot{}, fmt.Errorf(
			"%w: build %s request: %v", ErrClientRequest, action, err,
		)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)

	response, err := c.http.Do(request)
	if err != nil {
		return realtimev1.RuntimeSnapshot{}, preserveContextError(
			ctx, fmt.Errorf("%w: %v", ErrDependencyUnavailable, err),
		)
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxClientResponseBytes+1)
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return realtimev1.RuntimeSnapshot{}, decodeClientError(
			response.StatusCode, reader,
		)
	}
	var snapshot realtimev1.RuntimeSnapshot
	if err := json.NewDecoder(reader).Decode(&snapshot); err != nil {
		return realtimev1.RuntimeSnapshot{}, fmt.Errorf(
			"%w: decode %s response: %v", ErrInvalidResponse, action, err,
		)
	}
	if err := validate(snapshot); err != nil {
		return realtimev1.RuntimeSnapshot{}, err
	}
	return snapshot, nil
}

func (c *Client) ticket(ctx context.Context, sessionID string) (string, error) {
	token, err := c.tickets.Token(ctx, sessionID)
	if err != nil {
		return "", preserveContextError(
			ctx, fmt.Errorf("read realtime ticket: %w", err),
		)
	}
	if strings.TrimSpace(token) == "" {
		return "", ErrClientUnauthorized
	}
	return token, nil
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
	case string(realtimev1.ErrorConnectionNotFound):
		return ErrConnectionNotFound
	case string(realtimev1.ErrorRuntimeNotFound), "not_found":
		return ErrRuntimeNotFound
	case string(realtimev1.ErrorRuntimeOperationConflict):
		return ErrRuntimeOperationConflict
	case string(realtimev1.ErrorModeNotAvailable):
		return ErrModeNotAvailable
	case string(realtimev1.ErrorModeGenerationConflict):
		return ErrModeGenerationConflict
	case string(realtimev1.ErrorModeRuntimeInstanceMismatch):
		return ErrModeRuntimeInstanceMismatch
	case string(realtimev1.ErrorModeOperationConflict):
		return ErrModeOperationConflict
	case "conflict":
		return ErrClientConflict
	}
	if status >= http.StatusInternalServerError {
		return ErrDependencyUnavailable
	}
	return fmt.Errorf("%w: HTTP %d", ErrInvalidResponse, status)
}

func preserveContextError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return err
}

var _ TicketSource = TicketSourceFunc(nil)
