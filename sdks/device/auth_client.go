package device

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeviceSigner keeps the private key within the platform-specific secure
// element or key store. The SDK receives signatures, never key material.
type DeviceSigner interface {
	Sign(context.Context, []byte) ([]byte, error)
}

type DeviceSignerFunc func(context.Context, []byte) ([]byte, error)

func (f DeviceSignerFunc) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	if f == nil {
		return nil, ErrInvalidConfig
	}
	return f(ctx, payload)
}

type DeviceAuthClient struct {
	BaseURL     string
	DeviceID    string
	Signer      DeviceSigner
	HTTPClient  *http.Client
	MaxResponse int64
}

type DeviceAccessToken struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	DeviceID    string    `json:"device_id"`
}

func (c *DeviceAuthClient) Pair(ctx context.Context, pairingCode string) error {
	if c == nil || c.Signer == nil || strings.TrimSpace(c.DeviceID) == "" || strings.TrimSpace(pairingCode) == "" {
		return ErrInvalidConfig
	}
	signature, err := c.Signer.Sign(ctx, pairingPayload(c.DeviceID, pairingCode))
	if err != nil {
		return err
	}
	if len(signature) != ed25519.SignatureSize {
		return ErrInvalidResponse
	}
	return c.doJSON(ctx, http.MethodPost, "api/v1/devices/pair", map[string]string{"device_id": c.DeviceID, "pairing_code": pairingCode, "signature": base64.RawURLEncoding.EncodeToString(signature)}, nil)
}

func (c *DeviceAuthClient) Token(ctx context.Context) (DeviceAccessToken, error) {
	if c == nil || c.Signer == nil || strings.TrimSpace(c.DeviceID) == "" {
		return DeviceAccessToken{}, ErrInvalidConfig
	}
	var challenge struct {
		ChallengeID string    `json:"challenge_id"`
		Nonce       string    `json:"nonce"`
		ExpiresAt   time.Time `json:"expires_at"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "api/v1/device-auth/challenges", map[string]string{"device_id": c.DeviceID}, &challenge); err != nil {
		return DeviceAccessToken{}, err
	}
	if challenge.ChallengeID == "" || challenge.Nonce == "" || challenge.ExpiresAt.IsZero() {
		return DeviceAccessToken{}, ErrInvalidResponse
	}
	signature, err := c.Signer.Sign(ctx, challengePayload(challenge.ChallengeID, c.DeviceID, challenge.Nonce))
	if err != nil {
		return DeviceAccessToken{}, err
	}
	if len(signature) != ed25519.SignatureSize {
		return DeviceAccessToken{}, ErrInvalidResponse
	}
	var token DeviceAccessToken
	if err := c.doJSON(ctx, http.MethodPost, "api/v1/device-auth/tokens", map[string]string{"device_id": c.DeviceID, "challenge_id": challenge.ChallengeID, "signature": base64.RawURLEncoding.EncodeToString(signature)}, &token); err != nil {
		return DeviceAccessToken{}, err
	}
	if token.AccessToken == "" || token.DeviceID != c.DeviceID || token.ExpiresAt.IsZero() {
		return DeviceAccessToken{}, ErrInvalidResponse
	}
	return token, nil
}

// AccessToken implements the SDK's generic short-lived credential source so
// SessionStartClient can use a device token without receiving user credentials.
func (c *DeviceAuthClient) AccessToken(ctx context.Context) (string, error) {
	token, err := c.Token(ctx)
	if err != nil {
		return "", err
	}
	return token.AccessToken, nil
}

func (c *DeviceAuthClient) doJSON(ctx context.Context, method, path string, requestValue, responseValue any) error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return ErrInvalidConfig
	}
	body, err := json.Marshal(requestValue)
	if err != nil {
		return fmt.Errorf("%w: encode device request: %v", ErrInvalidRequest, err)
	}
	endpoint, err := url.JoinPath(strings.TrimRight(c.BaseURL, "/"), path)
	if err != nil {
		return fmt.Errorf("%w: device endpoint: %v", ErrInvalidConfig, err)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: device request: %v", ErrInvalidRequest, err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return decodeHTTPError(response.StatusCode, response.Body, c.maxResponse())
	}
	if responseValue == nil {
		return nil
	}
	if err := decodeBody(response.Body, responseValue, c.maxResponse()); err != nil {
		return fmt.Errorf("%w: device response: %v", ErrInvalidResponse, err)
	}
	return nil
}

func (c *DeviceAuthClient) maxResponse() int64 {
	if c.MaxResponse > 0 {
		return c.MaxResponse
	}
	return defaultMaxResponse
}
func pairingPayload(deviceID, pairingCode string) []byte {
	return []byte("lingow-device-pair-v1\n" + deviceID + "\n" + pairingCode)
}
func challengePayload(challengeID, deviceID, nonce string) []byte {
	return []byte("lingow-device-auth-v1\n" + challengeID + "\n" + deviceID + "\n" + nonce)
}
