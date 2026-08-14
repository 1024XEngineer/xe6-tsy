package languagesv1

import "testing"

func TestCommandConfigRequestValidate(t *testing.T) {
	t.Parallel()
	valid := CommandConfigRequest{SessionID: "session-1", CommandID: "command-1", SourceLanguage: "zh-CN", TargetLanguage: "en-US"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, mutate := range []func(*CommandConfigRequest){
		func(r *CommandConfigRequest) { r.SessionID = "" },
		func(r *CommandConfigRequest) { r.CommandID = "" },
		func(r *CommandConfigRequest) { r.SourceLanguage = "" },
		func(r *CommandConfigRequest) { r.TargetLanguage = "" },
		func(r *CommandConfigRequest) { r.TargetLanguage = "ZH-cn" },
	} {
		request := valid
		mutate(&request)
		if request.Validate() == nil {
			t.Fatalf("Validate(%#v) error = nil", request)
		}
	}
}
