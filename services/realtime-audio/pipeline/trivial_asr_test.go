package pipeline

import "testing"

func TestIsTrivialASRText(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"嗯", true},
		{"对。", true},
		{"Hmm", true},
		{"really", true},
		{"你一般是晚上听歌", false},
		{"Hello there", false},
		{"", true},
		{"。", true},
	}
	for _, test := range tests {
		if got := isTrivialASRText(test.text); got != test.want {
			t.Fatalf("isTrivialASRText(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}
