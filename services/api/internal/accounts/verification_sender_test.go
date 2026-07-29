package accounts

import "testing"

func TestMaskPhone(t *testing.T) {
	if got := maskPhone("+8613800000000"); got != "****0000" {
		t.Fatalf("maskPhone() = %q, want ****0000", got)
	}
	if got := maskPhone("123"); got != "****" {
		t.Fatalf("maskPhone(short) = %q, want ****", got)
	}
}

func TestMemoryVerificationSenderCapturesLatestCode(t *testing.T) {
	sender := NewMemoryVerificationSender(nil)
	if err := sender.SendCode(t.Context(), "+8613800000000", "123456"); err != nil {
		t.Fatalf("SendCode() error = %v", err)
	}
	code, ok := sender.LastCode("+8613800000000")
	if !ok || code != "123456" {
		t.Fatalf("LastCode() = (%q, %v), want (123456, true)", code, ok)
	}
}
