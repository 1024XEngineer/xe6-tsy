package recordstore

import (
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
)

func TestSessionTurnsCursorScopeIncludesFilters(t *testing.T) {
	baseQuery := recordsv1.ListTurnsQuery{Limit: 20, SourceLanguage: "zh-CN"}
	base := sessionTurnsCursorScope("account_01", "session_01", baseQuery)
	tests := []struct {
		name  string
		query recordsv1.ListTurnsQuery
	}{
		{name: "participant", query: recordsv1.ListTurnsQuery{Limit: 20, SourceLanguage: "zh-CN", ParticipantID: "participant_01"}},
		{name: "speaker", query: recordsv1.ListTurnsQuery{Limit: 20, SourceLanguage: "zh-CN", SpeakerCode: "speaker_01"}},
		{name: "status", query: recordsv1.ListTurnsQuery{Limit: 20, SourceLanguage: "zh-CN", AttributionStatus: recordsv1.AttributionPending}},
		{name: "source", query: recordsv1.ListTurnsQuery{Limit: 20, SourceLanguage: "en-US"}},
		{name: "target", query: recordsv1.ListTurnsQuery{Limit: 20, SourceLanguage: "zh-CN", TargetLanguage: "en-US"}},
		{name: "limit", query: recordsv1.ListTurnsQuery{Limit: 10, SourceLanguage: "zh-CN"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sessionTurnsCursorScope("account_01", "session_01", test.query); got == base {
				t.Fatalf("scope did not change with %s", test.name)
			}
		})
	}
	if got := sessionTurnsCursorScope("account_02", "session_01", baseQuery); got == base {
		t.Fatal("scope did not change with account")
	}
	if got := sessionTurnsCursorScope("account_01", "session_02", baseQuery); got == base {
		t.Fatal("scope did not change with session")
	}
}
