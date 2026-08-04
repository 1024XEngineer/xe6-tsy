package asr

import "testing"

func TestNormalizeLanguage(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"zh", "zh-CN"},
		{"zh-CN", "zh-CN"},
		{"en", "en-US"},
		{"en-US", "en-US"},
		{"en_GB", "en-GB"},
		{"yue", "zh-HK"},
		{"ru", "ru-RU"},
		{"ja", "ja-JP"},
		{"ko", "ko-KR"},
		{"fr", "fr-FR"},
		{"de", "de-DE"},
		{"pt", "pt-BR"},
		{"it", "it-IT"},
		{"es", "es-ES"},
	}
	for _, test := range tests {
		if got := NormalizeLanguage(test.in); got != test.want {
			t.Fatalf("NormalizeLanguage(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}
