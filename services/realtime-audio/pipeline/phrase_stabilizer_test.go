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
	if got := stabilizer.Advance(now.Add(defaultPhraseStableAfter)); len(got) != 0 {
		t.Fatalf("Advance() = %#v, want no live translation without punctuation", got)
	}
}

func TestPhraseStabilizerCommitsStableUnpunctuatedLiveChunk(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{StableAfter: 100 * time.Millisecond, LiveMinRunes: 4, LiveMaxRunes: 8})
	now := time.Unix(1700000000, 0)
	stabilizer.Observe("这是一个正在", now)
	stabilizer.Observe("这是一个正在进行的测试", now.Add(20*time.Millisecond))
	got := stabilizer.Advance(now.Add(120 * time.Millisecond))
	if len(got) != 1 || got[0].Text != "这是一个正在进行" {
		t.Fatalf("live chunk = %#v, want one stable semantic chunk", got)
	}
	if got := stabilizer.Flush("这是一个正在进行的测试"); len(got) != 1 || got[0].Text != "的测试" {
		t.Fatalf("remaining tail = %#v", got)
	}
}

func TestPhraseStabilizerKeepsWhitespaceDelimitedWordsIntact(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{
		StableAfter:  100 * time.Millisecond,
		LiveMinRunes: 8,
		LiveMaxRunes: 18,
	})
	now := time.Unix(1700000000, 0)
	finalText := "Today I want to tell you something important"

	stabilizer.Observe(finalText, now)
	got := stabilizer.Advance(now.Add(100 * time.Millisecond))
	if len(got) != 1 || got[0].Text != "Today I want to" {
		t.Fatalf("Advance() = %#v, want a complete-word live chunk", got)
	}
	if got := stabilizer.Flush(finalText); len(got) != 1 || got[0].Text != "tell you something important" {
		t.Fatalf("Flush() = %#v, want the intact remaining words", got)
	}
}

func TestPhraseStabilizerCommitsStablePrefixWhilePartialGrows(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	stabilizer.Observe("今天", now)
	if got := stabilizer.Advance(now.Add(defaultPhraseStableAfter - time.Millisecond)); len(got) != 0 {
		t.Fatalf("early Advance() = %#v", got)
	}
	stabilizer.Observe("今天很好", now.Add(200*time.Millisecond))
	if delay, ok := stabilizer.stabilityDelay(now.Add(200 * time.Millisecond)); !ok || delay != 300*time.Millisecond {
		t.Fatalf("stabilityDelay() = %v, %v, want 300ms, true", delay, ok)
	}
	if got := stabilizer.Advance(now.Add(defaultPhraseStableAfter)); len(got) != 0 {
		t.Fatalf("stable prefix Advance() = %#v, want no live translation without punctuation", got)
	}
}

func TestPhraseStabilizerDoesNotConsumeSingleCharacterCandidate(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	stabilizer.Observe("搞", now)
	if got := stabilizer.Advance(now.Add(defaultPhraseStableAfter)); len(got) != 0 {
		t.Fatalf("single-character Advance() = %#v", got)
	}
	stabilizer.Observe("搞了半天", now.Add(100*time.Millisecond))
	if got := stabilizer.Advance(now.Add(100*time.Millisecond + defaultPhraseStableAfter)); len(got) != 0 {
		t.Fatalf("expanded candidate Advance() = %#v, want no live translation without punctuation", got)
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

func TestPhraseStabilizerConsumesConfirmedFillerWithoutBlockingLaterPartials(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	if got := stabilizer.Observe("嗯，", now); len(got) != 0 {
		t.Fatalf("filler Observe() = %#v, want no phrase", got)
	}
	if stabilizer.consumed != "嗯，" {
		t.Fatalf("consumed = %q, want confirmed filler prefix", stabilizer.consumed)
	}
	if got := stabilizer.Observe("嗯，你好，", now.Add(time.Millisecond)); len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 1, Text: "你好，"}) {
		t.Fatalf("first translated phrase = %#v", got)
	}
	if got := stabilizer.Observe("嗯，你好，今天天气很好，", now.Add(2*time.Millisecond)); len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 2, Text: "今天天气很好，"}) {
		t.Fatalf("growing partial phrase = %#v", got)
	}
	if got := stabilizer.Flush("嗯，你好，今天天气很好，"); len(got) != 0 {
		t.Fatalf("final Flush() = %#v, want no duplicate", got)
	}
}

func TestPhraseStabilizerDoesNotConsumeUnpunctuatedFillerCandidate(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	if got := stabilizer.Observe("嗯", now); len(got) != 0 {
		t.Fatalf("Observe() = %#v, want no phrase", got)
	}
	if stabilizer.consumed != "" {
		t.Fatalf("consumed = %q, want unconfirmed candidate retained", stabilizer.consumed)
	}
	if got := stabilizer.Advance(now.Add(defaultPhraseStableAfter)); len(got) != 0 {
		t.Fatalf("Advance() = %#v, want no single-character phrase", got)
	}
}

func TestPhraseStabilizerDoesNotSilentlyConsumeMeaningfulShortPrefix(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{})
	now := time.Unix(1700000000, 0)

	if got := stabilizer.Observe("我，你好，", now); len(got) != 0 {
		t.Fatalf("Observe() = %#v, want short meaningful prefix retained", got)
	}
	got := stabilizer.Flush("我，你好，")
	if len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 1, Text: "我，你好，"}) {
		t.Fatalf("Flush() = %#v, want complete source phrase", got)
	}
}

func TestPhraseStabilizerAdvancesPunctuationConfirmedAfterLiveChunk(t *testing.T) {
	t.Parallel()
	stabilizer := NewPhraseStabilizer(PhraseStabilizerOptions{StableAfter: time.Millisecond, LiveMinRunes: 4, LiveMaxRunes: 40})
	now := time.Unix(1700000000, 0)

	first := "将科技含金量持续转化为发展含金量"
	stabilizer.Observe(first, now)
	if got := stabilizer.Advance(now.Add(time.Millisecond)); len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 1, Text: first}) {
		t.Fatalf("first live chunk = %#v", got)
	}
	if got := stabilizer.Observe(first+"，", now.Add(2*time.Millisecond)); len(got) != 0 {
		t.Fatalf("delayed punctuation = %#v, want cursor-only advance", got)
	}
	if stabilizer.consumed != first+"，" {
		t.Fatalf("consumed = %q, want delayed punctuation included", stabilizer.consumed)
	}
	finalText := first + "，为中国式现代化建设，"
	if got := stabilizer.Flush(finalText); len(got) != 1 || got[0] != (StablePhrase{SequenceNo: 2, Text: "为中国式现代化建设，"}) {
		t.Fatalf("Flush() = %#v, want only the unconsumed suffix", got)
	}
}
