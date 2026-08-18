package device

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeviceAuthClientPairsAndExchangesToken(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/devices/pair":
			var request struct {
				DeviceID    string `json:"device_id"`
				PairingCode string `json:"pairing_code"`
				Signature   string `json:"signature"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			signature, _ := base64.RawURLEncoding.DecodeString(request.Signature)
			if request.DeviceID != "dev_01" || request.PairingCode != "code_01" || !ed25519.Verify(publicKey, pairingPayload(request.DeviceID, request.PairingCode), signature) {
				t.Fatalf("invalid pair request: %#v", request)
			}
			_, _ = w.Write([]byte(`{}`))
		case "/api/v1/device-auth/challenges":
			_, _ = w.Write([]byte(`{"challenge_id":"challenge_01","nonce":"nonce_01","expires_at":"2026-08-18T10:02:00Z"}`))
		case "/api/v1/device-auth/tokens":
			var request struct {
				DeviceID    string `json:"device_id"`
				ChallengeID string `json:"challenge_id"`
				Signature   string `json:"signature"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			signature, _ := base64.RawURLEncoding.DecodeString(request.Signature)
			if request.DeviceID != "dev_01" || request.ChallengeID != "challenge_01" || !ed25519.Verify(publicKey, challengePayload(request.ChallengeID, request.DeviceID, "nonce_01"), signature) {
				t.Fatalf("invalid exchange request: %#v", request)
			}
			_, _ = w.Write([]byte(`{"access_token":"device_token","device_id":"dev_01","expires_at":"2026-08-18T10:15:00Z"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := &DeviceAuthClient{BaseURL: server.URL, DeviceID: "dev_01", Signer: DeviceSignerFunc(func(_ context.Context, payload []byte) ([]byte, error) { return ed25519.Sign(privateKey, payload), nil })}
	if err := client.Pair(t.Context(), "code_01"); err != nil {
		t.Fatalf("Pair() error = %v", err)
	}
	token, err := client.Token(t.Context())
	if err != nil || token.AccessToken != "device_token" || !token.ExpiresAt.Equal(time.Date(2026, 8, 18, 10, 15, 0, 0, time.UTC)) {
		t.Fatalf("AccessToken() = %#v, %v", token, err)
	}
}
