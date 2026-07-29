package accounts

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

// HMACIssuer implements the v1 short-lived JWT access token and opaque refresh
// token contract. Session activity is checked by the callback so logout and
// refresh rotation invalidate access tokens before their natural expiry.
type HMACIssuer struct {
	secret           []byte
	issuer           string
	audience         string
	accessTTL        time.Duration
	active           func(context.Context, string) (bool, error)
	activeForAccount func(context.Context, string, string) (bool, error)
}

// NewHMACIssuer creates an issuer with the legacy session-only activity
// callback. New production callers should prefer NewHMACIssuerWithAccount so
// a token's subject is checked against the session owner as well.
func NewHMACIssuer(secret, issuer, audience string, active func(context.Context, string) (bool, error)) (*HMACIssuer, error) {
	if len([]byte(secret)) < 32 || issuer == "" || audience == "" || active == nil {
		return nil, fmt.Errorf("%w: token configuration is incomplete", domain.ErrInvalidArgument)
	}
	return &HMACIssuer{secret: []byte(secret), issuer: issuer, audience: audience, accessTTL: time.Hour, active: active}, nil
}

// NewHMACIssuerWithAccount creates an issuer whose active-session check is
// bound to both the token session ID and account subject. This prevents a
// session that has been moved to another account from continuing to authorize
// requests with an old token subject.
func NewHMACIssuerWithAccount(secret, issuer, audience string, active func(context.Context, string, string) (bool, error)) (*HMACIssuer, error) {
	if len([]byte(secret)) < 32 || issuer == "" || audience == "" || active == nil {
		return nil, fmt.Errorf("%w: token configuration is incomplete", domain.ErrInvalidArgument)
	}
	return &HMACIssuer{secret: []byte(secret), issuer: issuer, audience: audience, accessTTL: time.Hour, activeForAccount: active}, nil
}

func (i *HMACIssuer) Issue(_ context.Context, account Account, session Session) (Tokens, error) {
	if i == nil || len(i.secret) == 0 || account.ID == "" || session.ID == "" {
		return Tokens{}, domain.ErrInvalidArgument
	}
	now := time.Now().UTC()
	expires := now.Add(i.accessTTL)
	header := encodePart(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload := encodePart(map[string]any{
		"iss": i.issuer, "aud": i.audience, "sub": account.ID, "sid": session.ID,
		"iat": now.Unix(), "exp": expires.Unix(),
	})
	unsigned := header + "." + payload
	refreshToken, err := newRefreshToken()
	if err != nil {
		return Tokens{}, fmt.Errorf("generate refresh token: %w", err)
	}
	return Tokens{AccessToken: unsigned + "." + i.sign(unsigned), RefreshToken: refreshToken, ExpiresAt: expires}, nil
}

func (i *HMACIssuer) HashRefreshToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (i *HMACIssuer) VerifyAccessToken(ctx context.Context, token string) (AccessTokenClaims, error) {
	if i == nil || len(i.secret) == 0 || i.issuer == "" || i.audience == "" {
		return AccessTokenClaims{}, domain.ErrUnauthorized
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return AccessTokenClaims{}, domain.ErrUnauthorized
	}
	unsigned := parts[0] + "." + parts[1]
	if !hmac.Equal([]byte(parts[2]), []byte(i.sign(unsigned))) {
		return AccessTokenClaims{}, domain.ErrUnauthorized
	}
	var payload struct {
		Issuer   string `json:"iss"`
		Audience string `json:"aud"`
		Subject  string `json:"sub"`
		Session  string `json:"sid"`
		Expires  int64  `json:"exp"`
		Issued   int64  `json:"iat"`
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(decoded, &payload) != nil {
		return AccessTokenClaims{}, domain.ErrUnauthorized
	}
	now := time.Now().Unix()
	if payload.Issuer != i.issuer || payload.Audience != i.audience || payload.Subject == "" || payload.Session == "" || payload.Expires <= now || payload.Issued > now+60 {
		return AccessTokenClaims{}, domain.ErrUnauthorized
	}
	if i.activeForAccount != nil {
		active, err := i.activeForAccount(ctx, payload.Session, payload.Subject)
		if err != nil {
			return AccessTokenClaims{}, err
		}
		if !active {
			return AccessTokenClaims{}, domain.ErrUnauthorized
		}
	} else if i.active != nil {
		active, err := i.active(ctx, payload.Session)
		if err != nil {
			return AccessTokenClaims{}, err
		}
		if !active {
			return AccessTokenClaims{}, domain.ErrUnauthorized
		}
	}
	return AccessTokenClaims{AccountID: payload.Subject, SessionID: payload.Session}, nil
}

func (i *HMACIssuer) sign(unsigned string) string {
	digest := hmac.New(sha256.New, i.secret)
	_, _ = digest.Write([]byte(unsigned))
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}

func encodePart(value any) string {
	payload, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func newRefreshToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

var _ TokenIssuer = (*HMACIssuer)(nil)
var _ AccessTokenVerifier = (*HMACIssuer)(nil)
