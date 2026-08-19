package pipeline

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultPhraseStableAfter = 500 * time.Millisecond
	defaultPhraseMinRunes    = 2
)

// PhraseStabilizerOptions control when a replaceable ASR snapshot becomes an
// immutable subtitle phrase. They are intentionally local to one utterance.
type PhraseStabilizerOptions struct {
	StableAfter time.Duration
	MinRunes    int
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
	stableAfter time.Duration
	minRunes    int
	consumed    string
	candidate   string
	candidateAt time.Time
	nextSeq     int64
}

// NewPhraseStabilizer constructs a per-utterance stabilizer with bounded defaults.
func NewPhraseStabilizer(options PhraseStabilizerOptions) *PhraseStabilizer {
	if options.StableAfter <= 0 {
		options.StableAfter = defaultPhraseStableAfter
	}
	if options.MinRunes <= 0 {
		options.MinRunes = defaultPhraseMinRunes
	}
	return &PhraseStabilizer{stableAfter: options.StableAfter, minRunes: options.MinRunes}
}

// Observe accepts a replaceable ASR snapshot. Punctuation commits immediately;
// unpunctuated text commits only after Advance confirms it remained unchanged.
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

// Advance commits an unchanged unpunctuated candidate after the configured stability window.
func (s *PhraseStabilizer) Advance(now time.Time) []StablePhrase {
	if s == nil || s.candidate == "" || s.candidateAt.IsZero() || now.Sub(s.candidateAt) < s.stableAfter {
		return nil
	}
	text := s.candidate
	s.clearCandidate()
	return s.consume(text)
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
		phrase := strings.TrimSpace(text[:end])
		phrases := s.consume(phrase)
		remaining := strings.TrimSpace(text[end:])
		more, tail := s.consumePunctuation(remaining)
		return append(phrases, more...), tail
	}
	return nil, text
}

func (s *PhraseStabilizer) consume(text string) []StablePhrase {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if s.consumed == "" {
		s.consumed = text
	} else {
		s.consumed += text
	}
	if utf8.RuneCountInString(strings.Trim(text, "。.!！?？，,;；:：、 ")) < s.minRunes || isTrivialASRText(text) {
		return nil
	}
	s.nextSeq++
	return []StablePhrase{{SequenceNo: s.nextSeq, Text: text}}
}

func (s *PhraseStabilizer) setCandidate(text string, now time.Time) {
	text = strings.TrimSpace(text)
	if text == s.candidate {
		return
	}
	if text == "" {
		s.clearCandidate()
		return
	}
	s.candidate = text
	s.candidateAt = now
}

func (s *PhraseStabilizer) clearCandidate() {
	s.candidate = ""
	s.candidateAt = time.Time{}
}

func phraseBoundary(value rune) bool {
	switch value {
	case '。', '.', '！', '!', '？', '?', '，', ',', '；', ';', '：', ':':
		return true
	default:
		return false
	}
}
