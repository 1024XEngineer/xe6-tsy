package accounts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerificationSenderFromEnvCheckedRejectsDevelopmentSenderInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	for _, value := range []string{"", "log"} {
		t.Setenv("VERIFICATION_SENDER", value)
		if _, err := VerificationSenderFromEnvChecked(); err == nil {
			t.Fatalf("sender %q accepted in production", value)
		}
	}
}

func TestHTTPVerificationSenderSendsProviderRequest(t *testing.T) {
	var gotAuth string
	var gotBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	t.Setenv("APP_ENV", "local")
	t.Setenv("VERIFICATION_SENDER", "http")
	t.Setenv("VERIFICATION_SMS_ENDPOINT", server.URL)
	t.Setenv("VERIFICATION_SMS_TOKEN", "provider-token")
	sender, err := VerificationSenderFromEnvChecked()
	if err != nil {
		t.Fatalf("VerificationSenderFromEnvChecked() error = %v", err)
	}
	if err := sender.SendCode(context.Background(), "+8613800000000", "123456"); err != nil {
		t.Fatalf("SendCode() error = %v", err)
	}
	if gotAuth != "Bearer provider-token" || gotBody["phone"] != "+8613800000000" || gotBody["code"] != "123456" {
		t.Fatalf("provider request = auth %q body %#v", gotAuth, gotBody)
	}
}

func TestVerificationSenderFromEnvCheckedRejectsMalformedEndpoint(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	t.Setenv("VERIFICATION_SENDER", "http")
	for _, endpoint := range []string{"", "not a URL", "file:///tmp/sms"} {
		t.Setenv("VERIFICATION_SMS_ENDPOINT", endpoint)
		if _, err := VerificationSenderFromEnvChecked(); err == nil || !strings.Contains(err.Error(), "VERIFICATION_SMS_ENDPOINT") {
			t.Fatalf("endpoint %q error = %v", endpoint, err)
		}
	}
}
