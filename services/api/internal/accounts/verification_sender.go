package accounts

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// LogVerificationSender delivers one-time codes to structured logs for local
// development. Phone numbers are masked so logs stay safe to share.
type LogVerificationSender struct{}

func (LogVerificationSender) SendCode(_ context.Context, phone, code string) error {
	slog.Info("phone verification code sent", "phone", maskPhone(phone), "code", code)
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

func maskPhone(phone string) string {
	if len(phone) <= 4 {
		return "****"
	}
	return fmt.Sprintf("****%s", phone[len(phone)-4:])
}
