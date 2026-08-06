package accounts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

// CredentialDigester derives non-reversible database values from the dedicated
// authentication pepper. It is intentionally separate from the JWT signing key
// and uses domain-separated inputs so a value from one column cannot be reused
// as a valid value for another.
type CredentialDigester struct{ pepper []byte }

func NewCredentialDigester(pepper string) (*CredentialDigester, error) {
	if len([]byte(pepper)) < 32 {
		return nil, fmt.Errorf("authentication pepper must be at least 32 bytes")
	}
	return &CredentialDigester{pepper: []byte(pepper)}, nil
}

func (d *CredentialDigester) PhoneHash(phone string) string {
	return d.sum("lingow.phone.v2\x00" + phone)
}

func (d *CredentialDigester) CodeHash(challengeID, code string) string {
	return d.sum("lingow.otp.v2\x00" + challengeID + "\x00" + code)
}

// EncryptLegacyPhoneHash keeps the one-time compatibility lookup value out of
// the database in plaintext. It is needed only while a v2 challenge is active
// so an account that still has the historical SHA-256 phone hash can be lazily
// upgraded on successful login.
func (d *CredentialDigester) EncryptLegacyPhoneHash(legacyHash string) (string, error) {
	block, err := aes.NewCipher(d.encryptionKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(legacyHash), nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func (d *CredentialDigester) DecryptLegacyPhoneHash(encoded string) (string, error) {
	ciphertext, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(d.encryptionKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", fmt.Errorf("legacy phone lookup ciphertext is invalid")
	}
	plain, err := gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (d *CredentialDigester) sum(value string) string {
	mac := hmac.New(sha256.New, d.pepper)
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func (d *CredentialDigester) encryptionKey() []byte {
	mac := hmac.New(sha256.New, d.pepper)
	_, _ = mac.Write([]byte("lingow.phone.legacy-encryption.v1"))
	return mac.Sum(nil)
}
