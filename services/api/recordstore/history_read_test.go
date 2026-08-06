package recordstore

import (
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestHistoryCursorScopeIncludesFilters(t *testing.T) {
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.FixedZone("offset", 8*60*60))
	to := from.Add(24 * time.Hour)
	baseQuery := recordsv1.ListTurnsQuery{Limit: 20, CreatedFrom: &from, CreatedTo: &to}
	base := historyCursorScope("account_01", baseQuery)
	tests := []struct {
		name  string
		query recordsv1.ListTurnsQuery
	}{
		{name: "session", query: recordsv1.ListTurnsQuery{Limit: 20, SessionID: "session_01", CreatedFrom: &from, CreatedTo: &to}},
		{name: "participant", query: recordsv1.ListTurnsQuery{Limit: 20, ParticipantID: "participant_01", CreatedFrom: &from, CreatedTo: &to}},
		{name: "source", query: recordsv1.ListTurnsQuery{Limit: 20, SourceLanguage: "zh-CN", CreatedFrom: &from, CreatedTo: &to}},
		{name: "target", query: recordsv1.ListTurnsQuery{Limit: 20, TargetLanguage: "en-US", CreatedFrom: &from, CreatedTo: &to}},
		{name: "limit", query: recordsv1.ListTurnsQuery{Limit: 10, CreatedFrom: &from, CreatedTo: &to}},
		{name: "from", query: recordsv1.ListTurnsQuery{Limit: 20, CreatedFrom: ptrTime(from.Add(time.Second)), CreatedTo: &to}},
		{name: "to", query: recordsv1.ListTurnsQuery{Limit: 20, CreatedFrom: &from, CreatedTo: ptrTime(to.Add(time.Second))}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := historyCursorScope("account_01", test.query); got == base {
				t.Fatalf("scope did not change with %s", test.name)
			}
		})
	}
	if got := historyCursorScope("account_02", baseQuery); got == base {
		t.Fatal("scope did not change with account")
	}
}

func TestHistoryCursorScopeNormalizesTimeZone(t *testing.T) {
	local := time.Date(2026, time.July, 1, 8, 0, 0, 123, time.FixedZone("offset", 8*60*60))
	utc := local.UTC()
	if first, second := historyCursorScope("account_01", recordsv1.ListTurnsQuery{Limit: 20, CreatedFrom: &local}), historyCursorScope("account_01", recordsv1.ListTurnsQuery{Limit: 20, CreatedFrom: &utc}); first != second {
		t.Fatalf("equivalent instants produced different scopes: %q != %q", first, second)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
