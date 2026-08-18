package devices

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

const deviceTokenTTL = 15 * time.Minute

type HMACIssuer struct {
	secret   []byte
	issuer   string
	audience string
	active   func(context.Context, string, string) error
	now      func() time.Time
}

func NewHMACIssuer(secret, issuer, audience string, active func(context.Context, string, string) error) (*HMACIssuer, error) {
	if len([]byte(secret)) < 32 || issuer == "" || audience == "" || active == nil {
		return nil, fmt.Errorf("%w: device token configuration is incomplete", domain.ErrInvalidArgument)
	}
	return &HMACIssuer{secret: []byte(secret), issuer: issuer, audience: audience, active: active, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (i *HMACIssuer) Issue(claims DeviceClaims) (Token, error) {
	if i == nil || claims.AccountID == "" || claims.DeviceID == "" {
		return Token{}, domain.ErrInvalidArgument
	}
	now := i.now().UTC()
	expiresAt := now.Add(deviceTokenTTL)
	header := deviceEncode(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload := deviceEncode(map[string]any{"iss": i.issuer, "aud": i.audience, "sub": claims.AccountID, "did": claims.DeviceID, "typ": "device", "iat": now.Unix(), "exp": expiresAt.Unix()})
	unsigned := header + "." + payload
	return Token{AccessToken: unsigned + "." + i.sign(unsigned), ExpiresAt: expiresAt, DeviceID: claims.DeviceID}, nil
}

func (i *HMACIssuer) Verify(ctx context.Context, token string) (DeviceClaims, error) {
	parts := strings.Split(token, ".")
	if i == nil || len(parts) != 3 || !hmac.Equal([]byte(parts[2]), []byte(i.sign(parts[0]+"."+parts[1]))) {
		return DeviceClaims{}, domain.ErrUnauthorized
	}
	var payload struct {
		Issuer    string `json:"iss"`
		Audience  string `json:"aud"`
		AccountID string `json:"sub"`
		DeviceID  string `json:"did"`
		Type      string `json:"typ"`
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(raw, &payload) != nil || payload.Issuer != i.issuer || payload.Audience != i.audience || payload.Type != "device" || payload.AccountID == "" || payload.DeviceID == "" || payload.ExpiresAt <= i.now().Unix() || payload.IssuedAt > i.now().Add(time.Minute).Unix() {
		return DeviceClaims{}, domain.ErrUnauthorized
	}
	if err := i.active(ctx, payload.DeviceID, payload.AccountID); err != nil {
		return DeviceClaims{}, err
	}
	return DeviceClaims{AccountID: payload.AccountID, DeviceID: payload.DeviceID, ExpiresAt: time.Unix(payload.ExpiresAt, 0).UTC()}, nil
}

func (i *HMACIssuer) sign(value string) string {
	mac := hmac.New(sha256.New, i.secret)
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func deviceEncode(value any) string {
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}
