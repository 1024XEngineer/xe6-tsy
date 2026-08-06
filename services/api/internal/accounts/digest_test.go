package accounts

import "testing"

func TestCredentialDigesterRequiresIndependentStrongPepper(t *testing.T) {
	if _, err := NewCredentialDigester("short"); err == nil {
		t.Fatal("NewCredentialDigester() accepted a short pepper")
	}
	digester, err := NewCredentialDigester("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewCredentialDigester() error = %v", err)
	}
	phoneHash := digester.PhoneHash("+8613800000000")
	if phoneHash == hashValue("+8613800000000") {
		t.Fatal("PhoneHash() fell back to the legacy SHA-256 value")
	}
	first := digester.CodeHash("challenge_one", "123456")
	if first == digester.CodeHash("challenge_two", "123456") {
		t.Fatal("CodeHash() does not bind the OTP to its challenge")
	}
	if first == digester.PhoneHash("123456") {
		t.Fatal("CodeHash() is not domain-separated from PhoneHash()")
	}
	legacy := hashValue("+8613800000000")
	protected, err := digester.EncryptLegacyPhoneHash(legacy)
	if err != nil {
		t.Fatalf("EncryptLegacyPhoneHash() error = %v", err)
	}
	if protected == legacy {
		t.Fatal("EncryptLegacyPhoneHash() stored the legacy SHA-256 value directly")
	}
	decrypted, err := digester.DecryptLegacyPhoneHash(protected)
	if err != nil || decrypted != legacy {
		t.Fatalf("DecryptLegacyPhoneHash() = (%q, %v), want (%q, nil)", decrypted, err, legacy)
	}
}
