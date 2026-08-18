package devices

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestServicePairsAndAuthenticatesBoundDevice(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	repository := newMemoryRepository()
	issuer, err := NewHMACIssuer("device-token-secret-must-be-at-least-32-bytes", "issuer", "device", repository.ActiveBound)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, issuer)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	service.random = deterministicRandom
	if _, err := service.Provision(t.Context(), "dev_01", "lingow-s3", publicKey); err != nil {
		t.Fatalf("Provision() error = %v", err)
	}

	code, _, err := service.CreatePairingCode(t.Context(), "acct_registered")
	if err != nil {
		t.Fatalf("CreatePairingCode() error = %v", err)
	}
	pairSignature := ed25519.Sign(privateKey, pairingPayload("dev_01", code))
	if _, err := service.Pair(t.Context(), "dev_01", code, pairSignature); err != nil {
		t.Fatalf("Pair() error = %v", err)
	}

	challenge, err := service.CreateChallenge(t.Context(), "dev_01")
	if err != nil {
		t.Fatalf("CreateChallenge() error = %v", err)
	}
	token, err := service.ExchangeChallenge(t.Context(), "dev_01", challenge.ID, ed25519.Sign(privateKey, challengePayload(challenge.ID, "dev_01", challenge.Nonce)))
	if err != nil {
		t.Fatalf("ExchangeChallenge() error = %v", err)
	}
	claims, err := service.Verify(t.Context(), token.AccessToken)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.AccountID != "acct_registered" || claims.DeviceID != "dev_01" {
		t.Fatalf("claims = %#v", claims)
	}
	if _, err := service.ExchangeChallenge(t.Context(), "dev_01", challenge.ID, ed25519.Sign(privateKey, challengePayload(challenge.ID, "dev_01", challenge.Nonce))); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("challenge replay error = %v, want unauthorized", err)
	}
}

func TestServiceRejectsInvalidPairingSignatureWithoutConsumingCode(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	repository := newMemoryRepository()
	issuer, err := NewHMACIssuer("device-token-secret-must-be-at-least-32-bytes", "issuer", "device", repository.ActiveBound)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, issuer)
	if err != nil {
		t.Fatal(err)
	}
	service.random = deterministicRandom
	if _, err := service.Provision(t.Context(), "dev_01", "lingow-s3", publicKey); err != nil {
		t.Fatal(err)
	}
	code, _, err := service.CreatePairingCode(t.Context(), "acct_registered")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Pair(t.Context(), "dev_01", code, make([]byte, ed25519.SignatureSize)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("invalid signature error = %v, want unauthorized", err)
	}
	if _, err := service.Pair(t.Context(), "dev_01", code, ed25519.Sign(privateKey, pairingPayload("dev_01", code))); err != nil {
		t.Fatalf("valid retry after invalid signature error = %v", err)
	}
}

func TestDeviceTokenIsRejectedAfterDeviceRevocation(t *testing.T) {
	repository := newMemoryRepository()
	repository.devices["dev_01"] = Device{DeviceID: "dev_01", ProductID: "lingow-s3", Status: StatusActive, AccountID: stringPointer("acct_registered")}
	issuer, err := NewHMACIssuer("device-token-secret-must-be-at-least-32-bytes", "issuer", "device", repository.ActiveBound)
	if err != nil {
		t.Fatal(err)
	}
	token, err := issuer.Issue(DeviceClaims{AccountID: "acct_registered", DeviceID: "dev_01"})
	if err != nil {
		t.Fatal(err)
	}
	repository.devices["dev_01"] = Device{DeviceID: "dev_01", ProductID: "lingow-s3", Status: StatusRevoked, AccountID: stringPointer("acct_registered")}
	if _, err := issuer.Verify(t.Context(), token.AccessToken); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("Verify() error = %v, want unauthorized", err)
	}
}

type memoryRepository struct {
	mu         sync.Mutex
	devices    map[string]Device
	codes      map[string]PairingCode
	challenges map[string]Challenge
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{devices: map[string]Device{}, codes: map[string]PairingCode{}, challenges: map[string]Challenge{}}
}
func (r *memoryRepository) Provision(_ context.Context, device Device) (Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.devices[device.DeviceID]; ok {
		if existing.ProductID != device.ProductID || string(existing.PublicKey) != string(device.PublicKey) {
			return Device{}, domain.ErrConflict
		}
		return existing, nil
	}
	r.devices[device.DeviceID] = device
	return device, nil
}
func (r *memoryRepository) GetActive(_ context.Context, id string) (Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[id]
	if !ok || device.Status != StatusActive {
		return Device{}, domain.ErrUnauthorized
	}
	return device, nil
}
func (r *memoryRepository) CanCreatePairingCode(_ context.Context, accountID string) error {
	if accountID != "acct_registered" {
		return domain.ErrForbidden
	}
	return nil
}
func (r *memoryRepository) ListBound(_ context.Context, accountID string) ([]Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]Device, 0)
	for _, device := range r.devices {
		if device.AccountID != nil && *device.AccountID == accountID {
			items = append(items, device)
		}
	}
	return items, nil
}
func (r *memoryRepository) Revoke(_ context.Context, accountID, deviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[deviceID]
	if !ok || device.AccountID == nil || *device.AccountID != accountID || device.Status != StatusActive {
		return domain.ErrNotFound
	}
	device.Status = StatusRevoked
	r.devices[deviceID] = device
	return nil
}
func (r *memoryRepository) CreatePairingCode(_ context.Context, code PairingCode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.codes[code.ID] = code
	return nil
}
func (r *memoryRepository) BindWithPairingCode(_ context.Context, deviceID string, codeHash []byte) (Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var code PairingCode
	found := false
	for _, value := range r.codes {
		if string(value.Hash) == string(codeHash) {
			code, found = value, true
			break
		}
	}
	if !found {
		return Device{}, domain.ErrUnauthorized
	}
	device := r.devices[deviceID]
	if device.AccountID != nil {
		return Device{}, domain.ErrConflict
	}
	device.AccountID = stringPointer(code.AccountID)
	r.devices[deviceID] = device
	delete(r.codes, code.ID)
	return device, nil
}
func (r *memoryRepository) CreateChallenge(_ context.Context, challenge Challenge) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.challenges[challenge.ID] = challenge
	return nil
}
func (r *memoryRepository) GetChallenge(_ context.Context, id, deviceID string) (Challenge, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	challenge, ok := r.challenges[id]
	if !ok || challenge.DeviceID != deviceID {
		return Challenge{}, domain.ErrUnauthorized
	}
	return challenge, nil
}
func (r *memoryRepository) ConsumeChallenge(_ context.Context, id, deviceID string) (Device, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	challenge, ok := r.challenges[id]
	if !ok || challenge.DeviceID != deviceID {
		return Device{}, domain.ErrUnauthorized
	}
	delete(r.challenges, id)
	return r.devices[deviceID], nil
}
func (r *memoryRepository) OwnsSession(context.Context, string, string, string) error { return nil }
func (r *memoryRepository) ActiveBound(_ context.Context, deviceID, accountID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	device, ok := r.devices[deviceID]
	if !ok || device.Status != StatusActive || device.AccountID == nil || *device.AccountID != accountID {
		return domain.ErrUnauthorized
	}
	return nil
}
func deterministicRandom(value []byte) error {
	for index := range value {
		value[index] = byte(index + 1)
	}
	return nil
}
func stringPointer(value string) *string { return &value }
