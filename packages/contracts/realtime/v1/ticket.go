package realtimev1

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ticketVersion         = "v1"
	minTicketSecretBytes  = 32
	defaultTicketLifetime = time.Minute
)

var (
	ErrTicketConfig          = errors.New("invalid realtime ticket configuration")
	ErrTicketInvalid         = errors.New("invalid realtime ticket")
	ErrTicketExpired         = errors.New("realtime ticket expired")
	ErrTicketSessionMismatch = errors.New("realtime ticket session mismatch")
)

// TicketClaims is the signed, short-lived authorization fact carried from API
// to realtime-audio. It scopes a bearer credential to exactly one Session.
type TicketClaims struct {
	SessionID string    `json:"session_id"`
	AccountID string    `json:"account_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TicketConfig supplies the shared HMAC settings used by API ticket issuance
// and realtime ticket validation.
type TicketConfig struct {
	Secret []byte
	TTL    time.Duration
	Now    func() time.Time
}

// HMACTicketCodec signs and verifies short-lived realtime tickets.
type HMACTicketCodec struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

func NewHMACTicketCodec(config TicketConfig) (*HMACTicketCodec, error) {
	if len(config.Secret) < minTicketSecretBytes {
		return nil, ErrTicketConfig
	}
	if config.TTL == 0 {
		config.TTL = defaultTicketLifetime
	}
	if config.TTL < 0 {
		return nil, ErrTicketConfig
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	secret := append([]byte(nil), config.Secret...)
	return &HMACTicketCodec{secret: secret, ttl: config.TTL, now: config.Now}, nil
}

func (c *HMACTicketCodec) Issue(sessionID string, accountID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(accountID) == "" {
		return "", ErrTicketInvalid
	}
	claims := TicketClaims{
		SessionID: sessionID,
		AccountID: accountID,
		ExpiresAt: c.now().UTC().Add(c.ttl),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("%w: encode claims: %v", ErrTicketInvalid, err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signed := ticketVersion + "." + encodedPayload
	signature := c.sign(signed)
	return signed + "." + signature, nil
}

func (c *HMACTicketCodec) Validate(token string, sessionID string) (TicketClaims, error) {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(sessionID) == "" {
		return TicketClaims{}, ErrTicketInvalid
	}
	version, payload, signature, ok := splitTicket(token)
	if !ok || version != ticketVersion {
		return TicketClaims{}, ErrTicketInvalid
	}
	signed := version + "." + payload
	if !hmac.Equal([]byte(signature), []byte(c.sign(signed))) {
		return TicketClaims{}, ErrTicketInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return TicketClaims{}, ErrTicketInvalid
	}
	var claims TicketClaims
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return TicketClaims{}, ErrTicketInvalid
	}
	if claims.SessionID != sessionID {
		return TicketClaims{}, ErrTicketSessionMismatch
	}
	if strings.TrimSpace(claims.AccountID) == "" || claims.ExpiresAt.IsZero() {
		return TicketClaims{}, ErrTicketInvalid
	}
	if !claims.ExpiresAt.After(c.now().UTC()) {
		return TicketClaims{}, ErrTicketExpired
	}
	return claims, nil
}

func (c *HMACTicketCodec) sign(value string) string {
	mac := hmac.New(sha256.New, c.secret)
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func splitTicket(token string) (string, string, string, bool) {
	first := strings.IndexByte(token, '.')
	last := strings.LastIndexByte(token, '.')
	if first <= 0 || last <= first+1 || last == len(token)-1 {
		return "", "", "", false
	}
	return token[:first], token[first+1 : last], token[last+1:], true
}
