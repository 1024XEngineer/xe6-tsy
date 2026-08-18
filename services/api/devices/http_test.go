package devices

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	internalwebapi "github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
)

func TestHandlerPairsAuthenticatesAndRevokesDevice(t *testing.T) {
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
	if _, err := service.Provision(t.Context(), "dev_01", "lingow-s3", publicKey); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service)
	mux := http.NewServeMux()
	accountAuth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			accountID := r.Header.Get("X-Test-Account")
			if accountID == "" {
				writeError(w, r, domain.ErrUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(internalwebapi.WithAccountID(r.Context(), accountID)))
		})
	}
	handler.Register(mux, accountAuth, handler.Authenticate)
	handler.RegisterSessions(mux, sessions.NewHandler(nil, nil), handler.Authenticate)
	mux.Handle("GET /device-auth-test", handler.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accountID, accountOK := internalwebapi.AccountIDFromContext(r.Context())
		deviceID, deviceOK := DeviceIDFromRequest(r)
		if !accountOK || !deviceOK || accountID != "acct_registered" || deviceID != "dev_01" {
			t.Fatal("device identity was not propagated")
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	call := func(method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
		var reader *bytes.Reader
		if body == nil {
			reader = bytes.NewReader(nil)
		} else if raw, ok := body.([]byte); ok {
			reader = bytes.NewReader(raw)
		} else {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(raw)
		}
		request := httptest.NewRequest(method, path, reader)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	accountHeaders := map[string]string{"X-Test-Account": "acct_registered"}
	pairing := call(http.MethodPost, "/api/v1/account/device-pairing-codes", nil, accountHeaders)
	if pairing.Code != http.StatusCreated {
		t.Fatalf("pairing status=%d body=%s", pairing.Code, pairing.Body.String())
	}
	var pairingBody struct {
		PairingCode string `json:"pairing_code"`
	}
	if err := json.NewDecoder(pairing.Body).Decode(&pairingBody); err != nil || pairingBody.PairingCode == "" {
		t.Fatalf("decode pairing response: %v, %#v", err, pairingBody)
	}
	signature := ed25519.Sign(privateKey, pairingPayload("dev_01", pairingBody.PairingCode))
	pair := call(http.MethodPost, "/api/v1/devices/pair", map[string]string{
		"device_id": "dev_01", "pairing_code": pairingBody.PairingCode, "signature": base64.RawURLEncoding.EncodeToString(signature),
	}, nil)
	if pair.Code != http.StatusOK {
		t.Fatalf("pair status=%d body=%s", pair.Code, pair.Body.String())
	}

	challengeResponse := call(http.MethodPost, "/api/v1/device-auth/challenges", map[string]string{"device_id": "dev_01"}, nil)
	var challenge struct {
		ID    string `json:"challenge_id"`
		Nonce string `json:"nonce"`
	}
	if challengeResponse.Code != http.StatusCreated || json.NewDecoder(challengeResponse.Body).Decode(&challenge) != nil {
		t.Fatalf("challenge status=%d body=%s", challengeResponse.Code, challengeResponse.Body.String())
	}
	tokenResponse := call(http.MethodPost, "/api/v1/device-auth/tokens", map[string]string{
		"device_id": "dev_01", "challenge_id": challenge.ID,
		"signature": base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, challengePayload(challenge.ID, "dev_01", challenge.Nonce))),
	}, nil)
	var token Token
	if tokenResponse.Code != http.StatusOK || json.NewDecoder(tokenResponse.Body).Decode(&token) != nil {
		t.Fatalf("token status=%d body=%s", tokenResponse.Code, tokenResponse.Body.String())
	}
	deviceHeaders := map[string]string{"Authorization": "Bearer " + token.AccessToken}
	if response := call(http.MethodGet, "/device-auth-test", nil, deviceHeaders); response.Code != http.StatusNoContent {
		t.Fatalf("authenticated status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/api/v1/account/devices", nil, accountHeaders); response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodDelete, "/api/v1/account/devices/dev_01", nil, accountHeaders); response.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/device-auth-test", nil, deviceHeaders); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/devices/pair", []byte(`{"unexpected":true}`), nil); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed pair status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/account/device-pairing-codes", []byte(`x`), accountHeaders); response.Code != http.StatusBadRequest {
		t.Fatalf("non-empty pairing request status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/account/device-pairing-codes", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated pairing request status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/account/device-pairing-codes", nil, map[string]string{"X-Test-Account": "acct_guest"}); response.Code != http.StatusForbidden {
		t.Fatalf("unregistered account pairing request status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/devices/pair", map[string]string{"device_id": "dev_01", "pairing_code": "code", "signature": "not-base64"}, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid pairing signature status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/devices/pair", map[string]string{"device_id": "missing", "pairing_code": "code", "signature": base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))}, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unknown device pairing status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/device-auth/challenges", map[string]string{"device_id": "missing"}, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unknown device challenge status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/device-auth/tokens", map[string]string{"device_id": "dev_01", "challenge_id": "missing", "signature": "not-base64"}, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid token signature status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/device-auth/tokens", map[string]string{"device_id": "missing", "challenge_id": "missing", "signature": base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))}, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unknown device token status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/api/v1/account/devices", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodDelete, "/api/v1/account/devices/missing", nil, accountHeaders); response.Code != http.StatusNotFound {
		t.Fatalf("missing device revoke status=%d body=%s", response.Code, response.Body.String())
	}
	invalidTokenHeaders := map[string]string{"Authorization": "Bearer malformed.token"}
	if response := call(http.MethodGet, "/device-auth-test", nil, invalidTokenHeaders); response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/device-auth-test", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerRejectsMalformedAndUnauthenticatedRequests(t *testing.T) {
	handler := NewHandler(nil)
	for _, test := range []struct {
		name   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{"list", handler.listDevices},
		{"revoke", handler.revokeDevice},
		{"pair", handler.pair},
		{"challenge", handler.createChallenge},
		{"token", handler.exchangeChallenge},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"unexpected":true}`))
			response := httptest.NewRecorder()
			test.handle(response, request)
			if response.Code != http.StatusUnauthorized && response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	response := httptest.NewRecorder()
	handler.Authenticate(nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated middleware status=%d", response.Code)
	}
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{"invalid", domain.ErrInvalidArgument, http.StatusBadRequest},
		{"unauthorized", domain.ErrUnauthorized, http.StatusUnauthorized},
		{"forbidden", domain.ErrForbidden, http.StatusForbidden},
		{"conflict", domain.ErrConflict, http.StatusConflict},
		{"not found", domain.ErrNotFound, http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("X-Request-ID", "req_test")
			writeError(response, request, test.err)
			if response.Code != test.want {
				t.Fatalf("status=%d, want %d", response.Code, test.want)
			}
		})
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.Body = nil
	if err := decodeJSON(request, &struct{}{}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("nil body decode error=%v", err)
	}
	if err := rejectNonEmptyBody(request); err != nil {
		t.Fatalf("nil body rejection error=%v", err)
	}
	if err := decodeJSON(httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{} {}`)), &struct{}{}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("multiple JSON values error=%v", err)
	}
	assertPanics(t, func() {
		NewHandler(nil).Register(http.NewServeMux(), func(next http.Handler) http.Handler { return next }, func(next http.Handler) http.Handler { return next })
	})
	assertPanics(t, func() {
		NewHandler(nil).RegisterSessions(http.NewServeMux(), sessions.NewHandler(nil, nil), func(next http.Handler) http.Handler { return next })
	})
}

func assertPanics(t *testing.T, call func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("call did not panic")
		}
	}()
	call()
}
