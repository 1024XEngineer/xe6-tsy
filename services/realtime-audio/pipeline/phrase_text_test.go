package pipeline

import "testing"

func TestStreamTextBoundaryAndCJKHelpers(t *testing.T) {
	tests := []struct {
		name        string
		value       rune
		boundary    bool
		unspacedCJK bool
	}{
		{name: "latin punctuation", value: '.', boundary: true},
		{name: "ideographic comma", value: '、', boundary: true},
		{name: "line break", value: '\n', boundary: true},
		{name: "latin letter", value: 'a'},
		{name: "han", value: '你', unspacedCJK: true},
		{name: "hiragana", value: 'あ', unspacedCJK: true},
		{name: "katakana", value: 'ア', unspacedCJK: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isStreamTextBoundary(test.value); got != test.boundary {
				t.Fatalf("isStreamTextBoundary(%q) = %v, want %v", test.value, got, test.boundary)
			}
			if got := isUnspacedCJKRune(test.value); got != test.unspacedCJK {
				t.Fatalf("isUnspacedCJKRune(%q) = %v, want %v", test.value, got, test.unspacedCJK)
			}
		})
	}
}
