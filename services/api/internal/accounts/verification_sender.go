package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// LogVerificationSender delivers one-time codes to structured logs for local
// development. Phone numbers are masked so logs stay safe to share.
type LogVerificationSender struct{}

func (LogVerificationSender) SendCode(_ context.Context, phone, code string) error {
	slog.Info("phone verification code sent", "phone", maskPhone(phone), "code", code)
	return nil
}

type HTTPVerificationSender struct {
	Endpoint string
	Token    string
	Client   *http.Client
}

func (s HTTPVerificationSender) SendCode(ctx context.Context, phone, code string) error {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	body, err := json.Marshal(struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}{Phone: phone, Code: code})
	if err != nil {
		return fmt.Errorf("encode verification request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create verification request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if s.Token != "" {
		request.Header.Set("Authorization", "Bearer "+s.Token)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send verification request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("verification provider returned HTTP %d", response.StatusCode)
	}
	return nil
}

// MemoryVerificationSender captures the most recent code per phone. Tests use
// it to exercise the full phone-login flow without an external SMS provider.
type MemoryVerificationSender struct {
	mu     sync.Mutex
	codes  map[string]string
	sender VerificationSender
}

func NewMemoryVerificationSender(fallback VerificationSender) *MemoryVerificationSender {
	return &MemoryVerificationSender{codes: make(map[string]string), sender: fallback}
}

func (m *MemoryVerificationSender) SendCode(ctx context.Context, phone, code string) error {
	m.mu.Lock()
	m.codes[phone] = code
	m.mu.Unlock()
	if m.sender != nil {
		return m.sender.SendCode(ctx, phone, code)
	}
	return nil
}

func (m *MemoryVerificationSender) LastCode(phone string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	code, ok := m.codes[phone]
	return code, ok
}

// VerificationSenderFromEnv selects the configured verification delivery adapter.
// Empty or "log" uses structured logs for local development.
func VerificationSenderFromEnv() VerificationSender {
	sender, _ := VerificationSenderFromEnvChecked()
	return sender
}

func VerificationSenderFromEnvChecked() (VerificationSender, error) {
	sender := strings.ToLower(strings.TrimSpace(os.Getenv("VERIFICATION_SENDER")))
	switch sender {
	case "", "log":
		if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") {
			return nil, fmt.Errorf("VERIFICATION_SENDER must be http in production")
		}
		return LogVerificationSender{}, nil
	case "http":
		endpoint := strings.TrimSpace(os.Getenv("VERIFICATION_SMS_ENDPOINT"))
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return nil, fmt.Errorf("VERIFICATION_SMS_ENDPOINT must be a valid HTTP or HTTPS URL")
		}
		if strings.EqualFold(strings.TrimSpace(os.Getenv("APP_ENV")), "production") && parsed.Scheme != "https" {
			return nil, fmt.Errorf("VERIFICATION_SMS_ENDPOINT must use HTTPS in production")
		}
		return HTTPVerificationSender{Endpoint: endpoint, Token: strings.TrimSpace(os.Getenv("VERIFICATION_SMS_TOKEN"))}, nil
	default:
		return nil, fmt.Errorf("unsupported VERIFICATION_SENDER %q", sender)
	}
}

func maskPhone(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return fmt.Sprintf("****%s", phone[len(phone)-4:])
}
