package languagesv1

import (
	"strings"
	"testing"
)

func TestCommandConfigRequestValidate(t *testing.T) {
	t.Parallel()
	expectedVersion := 1
	valid := CommandConfigRequest{
		SessionID: "session-1", CommandID: "command-1", SourceLanguage: "zh-CN", TargetLanguage: "en-US",
		OutputMode: InterpretationOutputModeSingle, ExpectedVersion: &expectedVersion,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*CommandConfigRequest)
	}{
		{name: "missing session", mutate: func(r *CommandConfigRequest) { r.SessionID = "" }},
		{name: "missing command", mutate: func(r *CommandConfigRequest) { r.CommandID = "" }},
		{name: "missing source language", mutate: func(r *CommandConfigRequest) { r.SourceLanguage = "" }},
		{name: "missing target language", mutate: func(r *CommandConfigRequest) { r.TargetLanguage = "" }},
		{name: "same language", mutate: func(r *CommandConfigRequest) { r.TargetLanguage = "ZH-cn" }},
		{name: "invalid output mode", mutate: func(r *CommandConfigRequest) { r.OutputMode = "speaker" }},
		{name: "invalid expected version", mutate: func(r *CommandConfigRequest) { version := 0; r.ExpectedVersion = &version }},
		{name: "command too long", mutate: func(r *CommandConfigRequest) { r.CommandID = strings.Repeat("c", MaxCommandIDLength+1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if request.Validate() == nil {
				t.Fatalf("Validate(%#v) error = nil", request)
			}
		})
	}
}

func TestCommandConfigSnapshotValidate(t *testing.T) {
	t.Parallel()
	valid := CommandConfigSnapshot{
		SessionID: "session-1", SourceLanguage: "zh-CN", TargetLanguage: "en-US",
		OutputMode: InterpretationOutputModeSingle, Version: 2,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*CommandConfigSnapshot)
	}{
		{name: "missing session", mutate: func(s *CommandConfigSnapshot) { s.SessionID = "" }},
		{name: "missing source language", mutate: func(s *CommandConfigSnapshot) { s.SourceLanguage = "" }},
		{name: "missing target language", mutate: func(s *CommandConfigSnapshot) { s.TargetLanguage = "" }},
		{name: "same language", mutate: func(s *CommandConfigSnapshot) { s.TargetLanguage = "ZH-cn" }},
		{name: "invalid output mode", mutate: func(s *CommandConfigSnapshot) { s.OutputMode = "speaker" }},
		{name: "invalid version", mutate: func(s *CommandConfigSnapshot) { s.Version = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := valid
			test.mutate(&snapshot)
			if snapshot.Validate() == nil {
				t.Fatalf("Validate(%#v) error = nil", snapshot)
			}
		})
	}
}
