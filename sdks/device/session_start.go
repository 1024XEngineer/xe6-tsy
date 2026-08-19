package device

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type VoiceSessionStatus string

const (
	VoiceSessionCreated VoiceSessionStatus = "created"
	VoiceSessionActive  VoiceSessionStatus = "active"
	VoiceSessionEnded   VoiceSessionStatus = "ended"
	VoiceSessionFailed  VoiceSessionStatus = "failed"
)

func (s VoiceSessionStatus) valid() bool {
	return s == VoiceSessionCreated || s == VoiceSessionActive || s == VoiceSessionEnded || s == VoiceSessionFailed
}

type VoiceSessionStartResult struct {
	ID        string             `json:"id"`
	AccountID string             `json:"account_id"`
	Status    VoiceSessionStatus `json:"status"`
	StartedAt *time.Time         `json:"started_at"`
	EndedAt   *time.Time         `json:"ended_at"`
	CreatedAt time.Time          `json:"created_at"`
}

// AccessTokenSource isolates API authentication from the device SDK core.
type AccessTokenSource interface {
	AccessToken(context.Context) (string, error)
}

type AccessTokenSourceFunc func(context.Context) (string, error)

func (f AccessTokenSourceFunc) AccessToken(ctx context.Context) (string, error) {
	if f == nil {
		return "", ErrInvalidConfig
	}
	return f(ctx)
}

// SessionStartClient calls services/api after the host has established media.
// It does not create sessions, mint realtime tickets, or own WebRTC lifecycle.
type SessionStartClient struct {
	BaseURL string
	// SessionPath defaults to api/v1/voice-sessions. Device firmware must set
	// it to api/v1/device/voice-sessions when using DeviceAuthClient.
	SessionPath string
	HTTPClient  *http.Client
	AccessToken AccessTokenSource
	MaxResponse int64
}

func (c *SessionStartClient) Start(
	ctx context.Context,
	sessionID string,
	initialMode Mode,
	idempotencyKey string,
) (VoiceSessionStartResult, error) {
	initialMode = initialMode.OrLegacyDefault()
	if c == nil || strings.TrimSpace(c.BaseURL) == "" || c.AccessToken == nil ||
		strings.TrimSpace(sessionID) == "" || strings.TrimSpace(idempotencyKey) == "" || !initialMode.Valid() {
		return VoiceSessionStartResult{}, ErrInvalidConfig
	}
	token, err := c.AccessToken.AccessToken(ctx)
	if err != nil {
		return VoiceSessionStartResult{}, err
	}
	if strings.TrimSpace(token) == "" {
		return VoiceSessionStartResult{}, ErrUnauthorized
	}
	body, err := json.Marshal(struct {
		InitialMode Mode `json:"initial_mode"`
	}{InitialMode: initialMode})
	if err != nil {
		return VoiceSessionStartResult{}, fmt.Errorf("%w: encode session start: %v", ErrInvalidRequest, err)
	}
	sessionPath := c.SessionPath
	if strings.TrimSpace(sessionPath) == "" {
		sessionPath = "api/v1/voice-sessions"
	}
	endpoint, err := url.JoinPath(strings.TrimRight(c.BaseURL, "/"), sessionPath, sessionID, "start")
	if err != nil {
		return VoiceSessionStartResult{}, fmt.Errorf("%w: start endpoint: %v", ErrInvalidConfig, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return VoiceSessionStartResult{}, fmt.Errorf("%w: session start request: %v", ErrInvalidRequest, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return VoiceSessionStartResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return VoiceSessionStartResult{}, decodeHTTPError(response.StatusCode, response.Body, c.maxResponse())
	}
	var result VoiceSessionStartResult
	if err := decodeBody(response.Body, &result, c.maxResponse()); err != nil {
		return VoiceSessionStartResult{}, fmt.Errorf("%w: session start: %v", ErrInvalidResponse, err)
	}
	if result.ID != sessionID || strings.TrimSpace(result.AccountID) == "" || !result.Status.valid() || result.CreatedAt.IsZero() {
		return VoiceSessionStartResult{}, ErrInvalidResponse
	}
	return result, nil
}

func (c *SessionStartClient) maxResponse() int64 {
	if c.MaxResponse > 0 {
		return c.MaxResponse
	}
	return defaultMaxResponse
}
