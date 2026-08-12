package command

import (
	"errors"
	"strings"
	"unicode"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

var ErrCommandNotAllowed = errors.New("voice command is not in the allowlist")

// Command is the bounded business intent produced by the deterministic parser.
// Text is canonical allowlist text rather than raw provider output.
type Command struct {
	Text       string
	TargetMode realtimev1.Mode
}

var commandAllowlist = map[string]Command{
	"开始同声传译": {Text: "开始同声传译", TargetMode: realtimev1.ModeInterpretation},
	"停止翻译":   {Text: "停止翻译", TargetMode: realtimev1.ModeAssistant},
}

// Parse accepts only the two product-approved commands. Normalization removes whitespace and
// sentence-ending punctuation that an ASR provider may add; it never uses fuzzy or semantic
// matching, so ordinary speech cannot expand the executable command surface.
func Parse(text string) (Command, error) {
	normalized := normalize(text)
	parsed, ok := commandAllowlist[normalized]
	if !ok {
		return Command{}, ErrCommandNotAllowed
	}
	return parsed, nil
}

func normalize(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, text)
	return strings.TrimRight(text, "，。！？,.!?")
}
