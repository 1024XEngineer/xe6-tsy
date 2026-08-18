package devices

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/oklog/ulid/v2"
)

const (
	pairingCodeTTL = 10 * time.Minute
	challengeTTL   = 2 * time.Minute
)

type Service struct {
	repository Repository
	issuer     TokenIssuer
	now        func() time.Time
	random     func([]byte) error
}

func NewService(repository Repository, issuer TokenIssuer) (*Service, error) {
	if repository == nil || issuer == nil {
		return nil, fmt.Errorf("%w: device repository and token issuer are required", domain.ErrInvalidArgument)
	}
	return &Service{repository: repository, issuer: issuer, now: func() time.Time { return time.Now().UTC() }, random: randomBytes}, nil
}

func (s *Service) Provision(ctx context.Context, deviceID, productID string, publicKey []byte) (Device, error) {
	if deviceID == "" || productID == "" || len(publicKey) != ed25519.PublicKeySize {
		return Device{}, domain.ErrInvalidArgument
	}
	now := s.now().UTC()
	return s.repository.Provision(ctx, Device{DeviceID: deviceID, ProductID: productID, PublicKey: append([]byte(nil), publicKey...), Status: StatusActive, CreatedAt: now})
}

func (s *Service) CreatePairingCode(ctx context.Context, accountID string) (string, time.Time, error) {
	if accountID == "" {
		return "", time.Time{}, domain.ErrUnauthorized
	}
	if err := s.repository.CanCreatePairingCode(ctx, accountID); err != nil {
		return "", time.Time{}, err
	}
	raw := make([]byte, 24)
	if err := s.random(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate device pairing code: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now().UTC()
	pairing := PairingCode{ID: "dpc_" + ulid.Make().String(), AccountID: accountID, Hash: hashSecret(code), ExpiresAt: now.Add(pairingCodeTTL), CreatedAt: now}
	if err := s.repository.CreatePairingCode(ctx, pairing); err != nil {
		return "", time.Time{}, err
	}
	return code, pairing.ExpiresAt, nil
}

func (s *Service) Pair(ctx context.Context, deviceID, code string, signature []byte) (Device, error) {
	if deviceID == "" || code == "" || len(signature) != ed25519.SignatureSize {
		return Device{}, domain.ErrInvalidArgument
	}
	device, err := s.repository.GetActive(ctx, deviceID)
	if err != nil {
		return Device{}, err
	}
	if !ed25519.Verify(ed25519.PublicKey(device.PublicKey), pairingPayload(deviceID, code), signature) {
		return Device{}, domain.ErrUnauthorized
	}
	return s.repository.BindWithPairingCode(ctx, deviceID, hashSecret(code))
}

func (s *Service) CreateChallenge(ctx context.Context, deviceID string) (Challenge, error) {
	if deviceID == "" {
		return Challenge{}, domain.ErrInvalidArgument
	}
	device, err := s.repository.GetActive(ctx, deviceID)
	if err != nil {
		return Challenge{}, err
	}
	if device.AccountID == nil || *device.AccountID == "" {
		return Challenge{}, domain.ErrUnauthorized
	}
	raw := make([]byte, 32)
	if err := s.random(raw); err != nil {
		return Challenge{}, fmt.Errorf("generate device authentication nonce: %w", err)
	}
	now := s.now().UTC()
	challenge := Challenge{ID: "dac_" + ulid.Make().String(), DeviceID: deviceID, Nonce: base64.RawURLEncoding.EncodeToString(raw), ExpiresAt: now.Add(challengeTTL), CreatedAt: now}
	if err := s.repository.CreateChallenge(ctx, challenge); err != nil {
		return Challenge{}, err
	}
	return challenge, nil
}

func (s *Service) ExchangeChallenge(ctx context.Context, deviceID, challengeID string, signature []byte) (Token, error) {
	if deviceID == "" || challengeID == "" || len(signature) != ed25519.SignatureSize {
		return Token{}, domain.ErrInvalidArgument
	}
	device, err := s.repository.GetActive(ctx, deviceID)
	if err != nil {
		return Token{}, err
	}
	challenge, err := s.peekChallenge(ctx, challengeID, deviceID)
	if err != nil {
		return Token{}, err
	}
	if !ed25519.Verify(ed25519.PublicKey(device.PublicKey), challengePayload(challenge.ID, deviceID, challenge.Nonce), signature) {
		return Token{}, domain.ErrUnauthorized
	}
	bound, err := s.repository.ConsumeChallenge(ctx, challengeID, deviceID)
	if err != nil {
		return Token{}, err
	}
	if bound.AccountID == nil || *bound.AccountID == "" {
		return Token{}, domain.ErrUnauthorized
	}
	return s.issuer.Issue(DeviceClaims{AccountID: *bound.AccountID, DeviceID: bound.DeviceID})
}

// peekChallenge reads the nonce before consuming it. The signature is checked
// first so an invalid request cannot burn a valid one-time challenge.
func (s *Service) peekChallenge(ctx context.Context, challengeID, deviceID string) (Challenge, error) {
	return s.repository.GetChallenge(ctx, challengeID, deviceID)
}

func (s *Service) Verify(ctx context.Context, token string) (DeviceClaims, error) {
	return s.issuer.Verify(ctx, token)
}
func (s *Service) OwnsSession(ctx context.Context, deviceID, accountID, sessionID string) error {
	return s.repository.OwnsSession(ctx, deviceID, accountID, sessionID)
}

func (s *Service) ListBound(ctx context.Context, accountID string) ([]Device, error) {
	if accountID == "" {
		return nil, domain.ErrUnauthorized
	}
	return s.repository.ListBound(ctx, accountID)
}

func (s *Service) Revoke(ctx context.Context, accountID, deviceID string) error {
	if accountID == "" || deviceID == "" {
		return domain.ErrInvalidArgument
	}
	return s.repository.Revoke(ctx, accountID, deviceID)
}

func hashSecret(value string) []byte { digest := sha256.Sum256([]byte(value)); return digest[:] }
func pairingPayload(deviceID, code string) []byte {
	return []byte("lingow-device-pair-v1\n" + deviceID + "\n" + code)
}
func challengePayload(challengeID, deviceID, nonce string) []byte {
	return []byte("lingow-device-auth-v1\n" + challengeID + "\n" + deviceID + "\n" + nonce)
}
func randomBytes(value []byte) error { _, err := rand.Read(value); return err }
