package pipeline

import (
	"testing"
	"time"
)

func TestPhraseStabilizerCommitsPunctuationImmediately(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	got := stabilizer.Observe("你好，今天怎么样", now)
	if len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 1, Text: "你好，"}) {
		t.Fatalf("Observe() = %#v", got)
	}
	if got := stabilizer.Advance(now.Add(defaultPhraseStableAfter)); len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 2, Text: "今天怎么样"}) {
		t.Fatalf("Advance() = %#v", got)
	}
}

func TestPhraseStabilizerWaitsForUnchangedPrefix(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	stabilizer.Observe("今天", now)
	if got := stabilizer.Advance(now.Add(defaultPhraseStableAfter - time.Millisecond)); len(got) != 0 {
		t.Fatalf("early Advance() = %#v", got)
	}
	stabilizer.Observe("今天很好", now.Add(200*time.Millisecond))
	if got := stabilizer.Advance(now.Add(defaultPhraseStableAfter)); len(got) != 0 {
		t.Fatalf("revised Advance() = %#v", got)
	}
	if got := stabilizer.Advance(now.Add(700 * time.Millisecond)); len(got) != 1 || got[0].Text != "今天很好" {
		t.Fatalf("stable Advance() = %#v", got)
	}
}

func TestPhraseStabilizerFlushesFinalTailWithoutDuplicates(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	if got := stabilizer.Observe("你好，", now); len(got) != 1 || got[0].Text != "你好，" {
		t.Fatalf("punctuation phrase = %#v", got)
	}
	if got := stabilizer.Flush("你好，世界"); len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 2, Text: "世界"}) {
		t.Fatalf("Flush() = %#v", got)
	}
	if got := stabilizer.Flush("你好，世界"); len(got) != 0 {
		t.Fatalf("second Flush() = %#v", got)
	}
}

func TestPhraseStabilizerPreservesWhitespaceInCommittedPrefix(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	if got := stabilizer.Observe("Hello, world", now); len(got) != 1 || got[0].Text != "Hello," {
		t.Fatalf("punctuation phrase = %#v", got)
	}
	if got := stabilizer.Flush("Hello, world"); len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 2, Text: "world"}) {
		t.Fatalf("Flush() = %#v", got)
	}
}

func TestPhraseStabilizerIgnoresRollbackAndNoise(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	stabilizer.Observe("嗯，", now)
	if got := stabilizer.Observe("嗯，你好，", now); len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 1, Text: "你好，"}) {
		t.Fatalf("filtered phrase = %#v", got)
	}
	if got := stabilizer.Flush("你"); len(got) != 0 {
		t.Fatalf("rollback Flush() = %#v", got)
	}
}
