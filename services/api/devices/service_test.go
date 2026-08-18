package devices

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

func TestServiceValidatesDeviceOperations(t *testing.T) {
	repository := newMemoryRepository()
	issuer, err := NewHMACIssuer("device-token-secret-must-be-at-least-32-bytes", "issuer", "device", repository.ActiveBound)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, issuer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(nil, issuer); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("NewService nil repository error=%v", err)
	}
	if _, err := service.Provision(t.Context(), "", "lingow-s3", make([]byte, ed25519.PublicKeySize)); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("invalid provision error=%v", err)
	}
	if _, _, err := service.CreatePairingCode(t.Context(), ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("empty account pairing error=%v", err)
	}
	if _, err := service.CreateChallenge(t.Context(), ""); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("empty device challenge error=%v", err)
	}
	if _, err := service.ExchangeChallenge(t.Context(), "dev_01", "", make([]byte, ed25519.SignatureSize)); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("invalid exchange error=%v", err)
	}
	repository.devices["dev_01"] = Device{DeviceID: "dev_01", ProductID: "lingow-s3", Status: StatusActive, AccountID: stringPointer("acct_registered")}
	if items, err := service.ListBound(t.Context(), "acct_registered"); err != nil || len(items) != 1 {
		t.Fatalf("ListBound() items=%#v error=%v", items, err)
	}
	if err := service.OwnsSession(t.Context(), "dev_01", "acct_registered", "vs_01"); err != nil {
		t.Fatalf("OwnsSession() error=%v", err)
	}
	if err := service.Revoke(t.Context(), "acct_registered", "dev_01"); err != nil {
		t.Fatalf("Revoke() error=%v", err)
	}
	if err := service.Revoke(t.Context(), "", "dev_01"); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("invalid revoke error=%v", err)
	}
	if Status("other").valid() {
		t.Fatal("unexpected status is valid")
	}
	if _, err := NewHMACIssuer("short", "issuer", "device", repository.ActiveBound); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("invalid issuer error=%v", err)
	}
	if _, err := issuer.Issue(DeviceClaims{}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("invalid token claims error=%v", err)
	}
	if _, err := issuer.Verify(t.Context(), "not-a-token"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("invalid token error=%v", err)
	}
	if _, err := service.ListBound(t.Context(), ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("empty account list error=%v", err)
	}
}

func TestServiceReturnsDependencyAndCryptographicErrors(t *testing.T) {
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
	service.random = func([]byte) error { return errors.New("entropy unavailable") }
	if _, _, err := service.CreatePairingCode(t.Context(), "acct_registered"); err == nil {
		t.Fatal("CreatePairingCode() error = nil")
	}
	if _, err := service.Provision(t.Context(), "dev_01", "lingow-s3", publicKey); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateChallenge(t.Context(), "dev_01"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("unbound device challenge error=%v", err)
	}
	service.random = deterministicRandom
	code, _, err := service.CreatePairingCode(t.Context(), "acct_registered")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Pair(t.Context(), "dev_01", code, make([]byte, ed25519.SignatureSize)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("invalid pairing signature error=%v", err)
	}
	if _, err := service.Pair(t.Context(), "dev_01", code, ed25519.Sign(privateKey, pairingPayload("dev_01", code))); err != nil {
		t.Fatal(err)
	}
	challenge, err := service.CreateChallenge(t.Context(), "dev_01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ExchangeChallenge(t.Context(), "dev_01", challenge.ID, make([]byte, ed25519.SignatureSize)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("invalid challenge signature error=%v", err)
	}
	if _, err := service.ExchangeChallenge(t.Context(), "dev_01", "missing", make([]byte, ed25519.SignatureSize)); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("missing challenge error=%v", err)
	}
}

func TestPostgresRepositoryRejectsMissingPool(t *testing.T) {
	repository := NewPostgresRepository(nil)
	device := Device{DeviceID: "dev_01", ProductID: "lingow-s3", PublicKey: make([]byte, ed25519.PublicKeySize), Status: StatusActive, CreatedAt: time.Now()}
	pairing := PairingCode{ID: "pairing_01", AccountID: "acct_01", Hash: make([]byte, 32), CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	challenge := Challenge{ID: "challenge_01", DeviceID: "dev_01", Nonce: "nonce", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	checks := []struct {
		name string
		err  error
		want error
	}{
		{"provision", errorOf(repository.Provision(t.Context(), device)), domain.ErrInvalidArgument},
		{"active", errorOf(repository.GetActive(t.Context(), "dev_01")), domain.ErrInvalidArgument},
		{"pairing allowed", repository.CanCreatePairingCode(t.Context(), "acct_01"), domain.ErrUnauthorized},
		{"list", errorOf(repository.ListBound(t.Context(), "acct_01")), domain.ErrUnauthorized},
		{"revoke", repository.Revoke(t.Context(), "acct_01", "dev_01"), domain.ErrInvalidArgument},
		{"create code", repository.CreatePairingCode(t.Context(), pairing), domain.ErrInvalidArgument},
		{"bind", errorOf(repository.BindWithPairingCode(t.Context(), "dev_01", make([]byte, 32))), domain.ErrInvalidArgument},
		{"create challenge", repository.CreateChallenge(t.Context(), challenge), domain.ErrInvalidArgument},
		{"get challenge", errorOf(repository.GetChallenge(t.Context(), "challenge_01", "dev_01")), domain.ErrInvalidArgument},
		{"consume challenge", errorOf(repository.ConsumeChallenge(t.Context(), "challenge_01", "dev_01")), domain.ErrInvalidArgument},
		{"owns", repository.OwnsSession(t.Context(), "dev_01", "acct_01", "vs_01"), domain.ErrUnauthorized},
		{"bind session", repository.BindSession(t.Context(), "dev_01", "acct_01", "vs_01"), domain.ErrInvalidArgument},
		{"active bound", repository.ActiveBound(t.Context(), "dev_01", "acct_01"), domain.ErrUnauthorized},
	}
	for _, check := range checks {
		if !errors.Is(check.err, check.want) {
			t.Errorf("%s error=%v, want %v", check.name, check.err, check.want)
		}
	}
}

func TestPostgresRepositoryMapsUnavailableDatabaseErrors(t *testing.T) {
	config, err := pgxpool.ParseConfig("postgres://devices:devices@localhost:5432/devices")
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.DialFunc = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("database is unavailable")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	repository := NewPostgresRepository(pool)
	now := time.Now().UTC()
	device := Device{DeviceID: "dev_01", ProductID: "lingow-s3", PublicKey: make([]byte, ed25519.PublicKeySize), Status: StatusActive, CreatedAt: now}
	pairing := PairingCode{ID: "pairing_01", AccountID: "acct_01", Hash: make([]byte, 32), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	challenge := Challenge{ID: "challenge_01", DeviceID: "dev_01", Nonce: "nonce", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	checks := []struct {
		name string
		err  error
	}{
		{"provision", errorOf(repository.Provision(t.Context(), device))},
		{"active", errorOf(repository.GetActive(t.Context(), "dev_01"))},
		{"pairing allowed", repository.CanCreatePairingCode(t.Context(), "acct_01")},
		{"list", errorOf(repository.ListBound(t.Context(), "acct_01"))},
		{"revoke", repository.Revoke(t.Context(), "acct_01", "dev_01")},
		{"create code", repository.CreatePairingCode(t.Context(), pairing)},
		{"bind", errorOf(repository.BindWithPairingCode(t.Context(), "dev_01", make([]byte, 32)))},
		{"create challenge", repository.CreateChallenge(t.Context(), challenge)},
		{"get challenge", errorOf(repository.GetChallenge(t.Context(), "challenge_01", "dev_01"))},
		{"consume challenge", errorOf(repository.ConsumeChallenge(t.Context(), "challenge_01", "dev_01"))},
		{"owns", repository.OwnsSession(t.Context(), "dev_01", "acct_01", "vs_01")},
		{"bind session", repository.BindSession(t.Context(), "dev_01", "acct_01", "vs_01")},
		{"active bound", repository.ActiveBound(t.Context(), "dev_01", "acct_01")},
	}
	for _, check := range checks {
		if check.err == nil {
			t.Errorf("%s error = nil", check.name)
		}
	}
}

func TestDevicePersistenceHelpersValidateRows(t *testing.T) {
	now := time.Now().UTC()
	validRow := deviceRowStub{scan: func(dest ...any) error {
		accountID := "acct_01"
		*dest[0].(*string) = "dev_01"
		*dest[1].(*string) = "lingow-s3"
		*dest[2].(*[]byte) = make([]byte, ed25519.PublicKeySize)
		*dest[3].(**string) = &accountID
		*dest[4].(*string) = string(StatusActive)
		*dest[5].(**time.Time) = nil
		*dest[6].(*time.Time) = now
		return nil
	}}
	if device, err := scanDevice(validRow); err != nil || device.DeviceID != "dev_01" {
		t.Fatalf("scan valid device=%#v error=%v", device, err)
	}
	if _, err := scanDevice(deviceRowStub{scan: func(...any) error { return pgx.ErrNoRows }}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("scan missing row error=%v", err)
	}
	if _, err := scanDevice(deviceRowStub{scan: func(dest ...any) error {
		if err := validRow.Scan(dest...); err != nil {
			return err
		}
		*dest[4].(*string) = "unexpected"
		return nil
	}}); err == nil {
		t.Fatal("invalid status error = nil")
	}
	if deviceError(nil) != nil || !errors.Is(deviceError(pgx.ErrNoRows), domain.ErrNotFound) || deviceError(errors.New("database failed")) == nil {
		t.Fatal("unexpected persistence error mapping")
	}
}

func errorOf[T any](_ T, err error) error { return err }

type deviceRowStub struct{ scan func(...any) error }

func (r deviceRowStub) Scan(dest ...any) error { return r.scan(dest...) }

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
