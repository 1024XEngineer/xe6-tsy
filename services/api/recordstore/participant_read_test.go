package recordstore

import "testing"

func TestParticipantCursorScopeIncludesOwnershipAndLimit(t *testing.T) {
	base := participantCursorScope("account_01", "session_01", 20)
	tests := []struct {
		name  string
		scope string
	}{
		{name: "account", scope: participantCursorScope("account_02", "session_01", 20)},
		{name: "session", scope: participantCursorScope("account_01", "session_02", 20)},
		{name: "limit", scope: participantCursorScope("account_01", "session_01", 10)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.scope == base {
				t.Fatalf("scope did not change with %s", test.name)
			}
		})
	}
}

func TestValidRecordPageSize(t *testing.T) {
	for _, test := range []struct {
		limit int
		want  bool
	}{{0, false}, {1, true}, {100, true}, {101, false}} {
		if got := validRecordPageSize(test.limit); got != test.want {
			t.Fatalf("validRecordPageSize(%d) = %t, want %t", test.limit, got, test.want)
		}
	}
}
