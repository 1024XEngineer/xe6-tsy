package pipeline

import (
	"strings"
	"unicode"
)

// streamTextBoundaryRunes is shared by validated translation chunking and
// playback coalescing. It intentionally includes ideographic comma and line
// breaks; ASR phraseBoundary has a narrower contract and remains separate.
const streamTextBoundaryRunes = ".!?,;:\u3002\uff01\uff1f\uff0c\uff1b\uff1a\u3001\n"

func isStreamTextBoundary(r rune) bool {
	return strings.ContainsRune(streamTextBoundaryRunes, r)
}

func isUnspacedCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}
