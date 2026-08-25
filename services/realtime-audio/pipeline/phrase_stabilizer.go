package pipeline

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultPhraseStableAfter  = 500 * time.Millisecond
	defaultPhraseMinRunes     = 2
	defaultPhraseLiveMinRunes = 8
	defaultPhraseLiveMaxRunes = 18
)

// PhraseStabilizerOptions control when a replaceable ASR snapshot becomes an
// immutable subtitle phrase. They are intentionally local to one utterance.
type PhraseStabilizerOptions struct {
	StableAfter  time.Duration
	MinRunes     int
	LiveMinRunes int
	LiveMaxRunes int
}

// StablePhrase is a source-language phrase accepted for ephemeral subtitle display.
type StablePhrase struct {
	SequenceNo int64
	Text       string
}

// PhraseStabilizer converts one utterance's replaceable ASR snapshots into ordered,
// immutable source phrases. Once text is consumed it is never emitted again, even if a
// later ASR revision rolls back before that boundary.
type PhraseStabilizer struct {
	stableAfter  time.Duration
	minRunes     int
	liveMinRunes int
	liveMaxRunes int
	consumed     string
	candidate    string
	candidateAt  time.Time
	nextSeq      int64
}

// NewPhraseStabilizer constructs a per-utterance stabilizer with bounded defaults.
func NewPhraseStabilizer(options PhraseStabilizerOptions) *PhraseStabilizer {
	if options.StableAfter <= 0 {
		options.StableAfter = defaultPhraseStableAfter
	}
	if options.MinRunes <= 0 {
		options.MinRunes = defaultPhraseMinRunes
	}
	if options.LiveMinRunes <= 0 {
		options.LiveMinRunes = defaultPhraseLiveMinRunes
	}
	if options.LiveMaxRunes <= 0 {
		options.LiveMaxRunes = defaultPhraseLiveMaxRunes
	}
	if options.LiveMaxRunes < options.LiveMinRunes {
		options.LiveMaxRunes = options.LiveMinRunes
	}
	return &PhraseStabilizer{stableAfter: options.StableAfter, minRunes: options.MinRunes,
		liveMinRunes: options.LiveMinRunes, liveMaxRunes: options.LiveMaxRunes}
}

// Observe accepts a replaceable ASR snapshot. Strong punctuation commits
// immediately; an unpunctuated tail enters a short stability window and may
// be committed by Advance once it reaches a semantic live-chunk size.
func (s *PhraseStabilizer) Observe(text string, now time.Time) []StablePhrase {
	remaining, ok := s.remaining(text)
	if !ok {
		s.clearCandidate()
		return nil
	}
	phrases, tail := s.consumePunctuation(remaining)
	s.setCandidate(tail, now)
	return phrases
}

func (s *PhraseStabilizer) Advance(now time.Time) []StablePhrase {
	if s == nil || s.candidate == "" {
		return nil
	}
	if delay, ok := s.stabilityDelay(now); !ok || delay > 0 {
		return nil
	}
	if utf8.RuneCountInString(s.candidate) < s.liveMinRunes {
		return nil
	}
	cut := takeRunes(s.candidate, s.liveMaxRunes)
	phrases := s.consume(cut)
	remaining := strings.TrimSpace(strings.TrimPrefix(s.candidate, cut))
	if remaining == "" {
		s.clearCandidate()
	} else {
		s.candidate = remaining
		s.candidateAt = now
	}
	return phrases
}

func takeRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	count := 0
	for index := range value {
		if count == limit {
			return value[:index]
		}
		count++
	}
	return value
}

// Flush consumes every remaining final ASR text segment. A final revision that no longer
// extends a committed phrase is ignored so it cannot duplicate already displayed subtitles.
func (s *PhraseStabilizer) Flush(text string) []StablePhrase {
	remaining, ok := s.remaining(text)
	if !ok {
		s.clearCandidate()
		return nil
	}
	phrases, tail := s.consumePunctuation(remaining)
	s.clearCandidate()
	return append(phrases, s.consume(tail)...)
}

func (s *PhraseStabilizer) remaining(text string) (string, bool) {
	if s == nil {
		return "", false
	}
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, s.consumed) {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(text, s.consumed)), true
}

func (s *PhraseStabilizer) consumePunctuation(text string) ([]StablePhrase, string) {
	for index, runeValue := range text {
		if !phraseBoundary(runeValue) {
			continue
		}
		end := index + utf8.RuneLen(runeValue)
		for end < len(text) {
			next, size := utf8.DecodeRuneInString(text[end:])
			if !unicode.IsSpace(next) {
				break
			}
			end += size
		}
		segment := text[:end]
		phrases := s.consume(segment)
		if len(phrases) == 0 {
			if !isIgnorableConfirmedGap(segment) {
				// Keep an unconsumed short but meaningful segment attached to the
				// tail. Emitting a later phrase would otherwise move the source
				// cursor past a prefix that it never recorded.
				return nil, text
			}
			s.appendConsumed(segment)
		}
		remaining := text[end:]
		more, tail := s.consumePunctuation(remaining)
		return append(phrases, more...), tail
	}
	return nil, text
}

func (s *PhraseStabilizer) consume(text string) []StablePhrase {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	displayText := strings.TrimSpace(text)
	if utf8.RuneCountInString(strings.Trim(displayText, "。.!！?？，,;；:：、 ")) < s.minRunes || isTrivialASRText(displayText) {
		// Do not advance consumed for a too-short candidate. Chinese ASR often
		// emits one character before extending it; consuming that character here
		// would make the later, longer snapshot impossible to translate.
		return nil
	}
	s.appendConsumed(text)
	s.nextSeq++
	return []StablePhrase{{SequenceNo: s.nextSeq, Text: displayText}}
}

func (s *PhraseStabilizer) appendConsumed(text string) {
	if s.consumed == "" {
		s.consumed = text
		return
	}
	s.consumed += text
}

func (s *PhraseStabilizer) setCandidate(text string, now time.Time) {
	text = strings.TrimSpace(text)
	if text == "" {
		s.clearCandidate()
		return
	}
	if s.candidate != "" {
		// Preserve the original stability timestamp while the recognizer only
		// appends to the confirmed tail. Keep the complete tail so a live chunk
		// cannot discard characters that arrived after the first snapshot.
		if strings.HasPrefix(text, s.candidate) {
			s.candidate = text
			return
		}
		// A revision invalidates the previous tail. Retain only the shared
		// prefix and restart its stability window.
		if prefix := commonPhrasePrefix(s.candidate, text); prefix != "" {
			s.candidate = prefix
			s.candidateAt = now
			return
		}
	}
	s.candidate = text
	s.candidateAt = now
}

func commonPhrasePrefix(left, right string) string {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	limit := len(leftRunes)
	if len(rightRunes) < limit {
		limit = len(rightRunes)
	}
	for index := 0; index < limit; index++ {
		if leftRunes[index] != rightRunes[index] {
			return string(leftRunes[:index])
		}
	}
	return string(leftRunes[:limit])
}

func (s *PhraseStabilizer) clearCandidate() {
	s.candidate = ""
	s.candidateAt = time.Time{}
}

func (s *PhraseStabilizer) stabilityDelay(now time.Time) (time.Duration, bool) {
	if s == nil || s.candidate == "" || s.candidateAt.IsZero() {
		return 0, false
	}
	delay := s.candidateAt.Add(s.stableAfter).Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func phraseBoundary(value rune) bool {
	switch value {
	case '。', '.', '！', '!', '？', '?', '，', ',', '；', ';', '：', ':':
		return true
	default:
		return false
	}
}

// isIgnorableConfirmedGap accepts punctuation-only separators and known
// hesitation tokens after ASR has confirmed their boundary. A separator may
// arrive after an unpunctuated live chunk was already consumed; advancing the
// source cursor keeps later stable phrases aligned with the final transcript.
// Meaningful short replies such as "yes", "好", and "对" remain source text.
func isIgnorableConfirmedGap(text string) bool {
	var token strings.Builder
	sawBoundary := false
	for _, value := range text {
		if phraseBoundary(value) {
			confirmed := strings.TrimSpace(token.String())
			if confirmed != "" && !isPhraseFillerToken(confirmed) {
				return false
			}
			sawBoundary = true
			token.Reset()
			continue
		}
		if unicode.IsSpace(value) && token.Len() == 0 {
			continue
		}
		token.WriteRune(value)
	}
	return sawBoundary && strings.TrimSpace(token.String()) == ""
}

func isPhraseFillerToken(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "嗯", "嗯嗯", "啊", "呃", "额", "哎", "欸", "诶", "哦", "噢", "喔",
		"咳", "咳咳", "嗯哼", "mm", "mmm", "mhm", "uh", "uhh", "um", "umm",
		"ah", "oh", "hmm", "hm", "huh", "sigh", "ahem":
		return true
	default:
		return false
	}
}
