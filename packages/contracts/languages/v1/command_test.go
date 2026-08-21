package languagesv1

import (
	"strings"
	"testing"
)

func TestCommandConfigRequestValidate(t *testing.T) {
	t.Parallel()
	valid := CommandConfigRequest{SessionID: "session-1", CommandID: "command-1", SourceLanguage: "zh-CN", TargetLanguage: "en-US"}
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
