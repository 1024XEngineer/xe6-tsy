package recordstore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCursorCodecRoundTrip(t *testing.T) {
	codec, err := NewCursorCodec([]byte("test-cursor-key"))
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	tests := []struct {
		name   string
		cursor Cursor
	}{
		{
			name: "participants",
			cursor: Cursor{
				Kind: CursorParticipants, Scope: CursorScope(CursorParticipants, url.Values{"account_id": {"account_01"}}),
				SpeakerCode: "speaker_01", ID: "participant_01",
			},
		},
		{
			name: "session turns",
			cursor: Cursor{
				Kind: CursorSessionTurns, Scope: CursorScope(CursorSessionTurns, url.Values{"session_id": {"session_01"}}),
				SequenceNo: 1, ID: "turn_01",
			},
		},
		{
			name: "history",
			cursor: Cursor{
				Kind: CursorHistory, Scope: CursorScope(CursorHistory, url.Values{"account_id": {"account_01"}, "source_language": {"zh-CN"}}),
				CreatedAt: time.Date(2026, time.July, 27, 10, 0, 0, 123456789, time.UTC), ID: "turn_01",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := codec.Encode(test.cursor)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			got, err := codec.Decode(encoded, test.cursor.Kind, test.cursor.Scope)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got != test.cursor {
				t.Fatalf("Decode() = %#v, want %#v", got, test.cursor)
			}
		})
	}
}

func TestCursorCodecRejectsInvalidPositionFields(t *testing.T) {
	codec, err := NewCursorCodec([]byte("test-cursor-key"))
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		cursor Cursor
	}{
		{name: "missing scope", cursor: Cursor{Kind: CursorParticipants, SpeakerCode: "speaker_01", ID: "participant_01"}},
		{name: "missing ID", cursor: Cursor{Kind: CursorParticipants, Scope: "scope", SpeakerCode: "speaker_01"}},
		{name: "participant missing speaker", cursor: Cursor{Kind: CursorParticipants, Scope: "scope", ID: "participant_01"}},
		{name: "participant has sequence", cursor: Cursor{Kind: CursorParticipants, Scope: "scope", SpeakerCode: "speaker_01", SequenceNo: 1, ID: "participant_01"}},
		{name: "participant has time", cursor: Cursor{Kind: CursorParticipants, Scope: "scope", SpeakerCode: "speaker_01", CreatedAt: now, ID: "participant_01"}},
		{name: "session turn has speaker", cursor: Cursor{Kind: CursorSessionTurns, Scope: "scope", SpeakerCode: "speaker_01", SequenceNo: 1, ID: "turn_01"}},
		{name: "session turn sequence zero", cursor: Cursor{Kind: CursorSessionTurns, Scope: "scope", ID: "turn_01"}},
		{name: "session turn has time", cursor: Cursor{Kind: CursorSessionTurns, Scope: "scope", SequenceNo: 1, CreatedAt: now, ID: "turn_01"}},
		{name: "history has speaker", cursor: Cursor{Kind: CursorHistory, Scope: "scope", SpeakerCode: "speaker_01", CreatedAt: now, ID: "turn_01"}},
		{name: "history has sequence", cursor: Cursor{Kind: CursorHistory, Scope: "scope", SequenceNo: 1, CreatedAt: now, ID: "turn_01"}},
		{name: "history missing time", cursor: Cursor{Kind: CursorHistory, Scope: "scope", ID: "turn_01"}},
		{name: "unknown kind", cursor: Cursor{Kind: "other", Scope: "scope", ID: "record_01"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := codec.Encode(test.cursor); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("Encode() error = %v, want invalid cursor", err)
			}
		})
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
	if first == CursorScope(CursorHistory, url.Values{"filter": {"a", "b"}, "session_id": {"session_01"}}) {
		t.Fatal("CursorScope() returned the same scope for distinct cursor kinds")
	}
}

func TestCursorCodecRejectsMalformedSignedPayloads(t *testing.T) {
	codec, err := NewCursorCodec([]byte("test-cursor-key"))
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	scope := "scope"
	valid := Cursor{Kind: CursorSessionTurns, Scope: scope, SequenceNo: 1, ID: "turn_01"}
	validPayload, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal valid cursor: %v", err)
	}
	tests := []struct {
		name  string
		value string
	}{
		{name: "missing part", value: "v1.payload"},
		{name: "unknown version", value: "v2.payload.mac"},
		{name: "invalid base64", value: "v1.%.mac"},
		{name: "unknown JSON field", value: signedCursor(codec, `{"kind":"session_turns","scope":"scope","sequence_no":1,"id":"turn_01","extra":true}`)},
		{name: "multiple JSON values", value: signedCursor(codec, string(validPayload)+` {}`)},
		{name: "invalid position", value: signedCursor(codec, `{"kind":"session_turns","scope":"scope","id":"turn_01"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := codec.Decode(test.value, CursorSessionTurns, scope); !errors.Is(err, ErrInvalidCursor) {
				t.Fatalf("Decode() error = %v, want invalid cursor", err)
			}
		})
	}
	if _, err := codec.Decode(signedCursor(codec, string(validPayload)), CursorSessionTurns, ""); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("Decode() with empty expected scope error = %v, want invalid cursor", err)
	}
}

func TestCursorCodecCopiesSigningKey(t *testing.T) {
	key := []byte("test-cursor-key")
	codec, err := NewCursorCodec(key)
	if err != nil {
		t.Fatalf("NewCursorCodec() error = %v", err)
	}
	cursor := Cursor{Kind: CursorSessionTurns, Scope: "scope", SequenceNo: 1, ID: "turn_01"}
	encoded, err := codec.Encode(cursor)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	key[0] = 'x'
	if _, err := codec.Decode(encoded, cursor.Kind, cursor.Scope); err != nil {
		t.Fatalf("Decode() after source key mutation error = %v", err)
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

func signedCursor(codec *CursorCodec, payload string) string {
	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return strings.Join([]string{cursorVersion, encodedPayload, codec.signature(encodedPayload)}, ".")
}
