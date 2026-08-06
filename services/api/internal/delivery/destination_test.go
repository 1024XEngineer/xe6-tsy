package delivery

import (
	"encoding/base64"
	"testing"
)

func TestDecodeDestinationKeyAcceptsDocumentedURLSafeEncoding(t *testing.T) {
	want := make([]byte, 32)
	for index := range want {
		want[index] = byte(index + 128)
	}

	encoded := base64.RawURLEncoding.EncodeToString(want)
	got, err := DecodeDestinationKey(encoded)
	if err != nil {
		t.Fatalf("DecodeDestinationKey() error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("DecodeDestinationKey() returned the wrong key")
	}
}

func TestDecodeDestinationKeyRejectsWrongLength(t *testing.T) {
	encoded := base64.RawURLEncoding.EncodeToString(make([]byte, 31))
	if _, err := DecodeDestinationKey(encoded); err == nil {
		t.Fatal("DecodeDestinationKey() error = nil, want length error")
	}
}
