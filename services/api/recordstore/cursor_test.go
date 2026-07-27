package recordstore

import (
	"errors"
	"net/url"
	"testing"
	"time"
)

func TestCursorCodecRoundTrip(t *testing.T) {
	codec, err := NewCursorCodec([]byte("test-cursor-key"))
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	scope := CursorScope(CursorHistory, url.Values{"account_id": {"account_01"}, "source_language": {"zh-CN"}})
	want := Cursor{
		Kind:      CursorHistory,
		Scope:     scope,
		CreatedAt: time.Date(2026, time.July, 27, 10, 0, 0, 123456789, time.UTC),
		ID:        "turn_01",
	}

	encoded, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	got, err := codec.Decode(encoded, CursorHistory, scope)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.Kind != want.Kind || got.Scope != want.Scope || got.ID != want.ID || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("Decode() = %#v, want %#v", got, want)
	}
}

func TestCursorCodecRejectsInvalidCursor(t *testing.T) {
	codec, err := NewCursorCodec([]byte("test-cursor-key"))
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	scope := CursorScope(CursorSessionTurns, url.Values{"session_id": {"session_01"}})
	encoded, err := codec.Encode(Cursor{
		Kind:       CursorSessionTurns,
		Scope:      scope,
		SequenceNo: 3,
		ID:         "turn_03",
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	tests := []struct {
		name          string
		cursor        string
		expectedKind  CursorKind
		expectedScope string
	}{
		{name: "tampered signature", cursor: tamperCursorSignature(encoded), expectedKind: CursorSessionTurns, expectedScope: scope},
		{name: "wrong kind", cursor: encoded, expectedKind: CursorHistory, expectedScope: scope},
		{name: "wrong scope", cursor: encoded, expectedKind: CursorSessionTurns, expectedScope: CursorScope(CursorSessionTurns, url.Values{"session_id": {"session_02"}})},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := codec.Decode(test.cursor, test.expectedKind, test.expectedScope)
			if !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("Decode() error = %v, want errors.Is(_, ErrInvalidCursor)", err)
			}
		})
	}
}

func TestCursorScopeIsIndependentOfValueOrder(t *testing.T) {
	first := CursorScope(CursorParticipants, url.Values{"session_id": {"session_01"}, "filter": {"b", "a"}})
	second := CursorScope(CursorParticipants, url.Values{"filter": {"a", "b"}, "session_id": {"session_01"}})
	if first != second {
		t.Fatalf("CursorScope() = %q and %q, want identical scopes", first, second)
	}
}

func TestNewCursorCodecRejectsEmptyKey(t *testing.T) {
	_, err := NewCursorCodec(nil)
	if err == nil {
		t.Fatal("NewCursorCodec() error = nil, want empty key error")
	}
}

func tamperCursorSignature(cursor string) string {
	if cursor[len(cursor)-1] == 'a' {
		return cursor[:len(cursor)-1] + "b"
	}
	return cursor[:len(cursor)-1] + "a"
}
