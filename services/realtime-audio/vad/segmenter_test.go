package vad

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/audio"
)

type fakeClassifier struct {
	speech bool
}

func (f fakeClassifier) Speech(audio.Frame) bool { return f.speech }

func TestSegmenterEmitsOneFinalAfterSilenceTimeout(t *testing.T) {
	segmenter := newTestSegmenter(t, fakeClassifier{speech: true})
	first := testFrame(t, 1, time.Unix(10, 0))
	second := testFrame(t, 2, time.Unix(10, 100_000_000))
	opened, err := segmenter.Push(context.Background(), first)
	if err != nil {
		t.Fatalf("Push(first) error = %v", err)
	}
	if len(opened) != 2 || opened[0].Type != EventOpened || opened[1].Type != EventAudio {
		t.Fatalf("Push(first) events = %#v, want opened and audio", opened)
	}
	if _, err := segmenter.Push(context.Background(), second); err != nil {
		t.Fatalf("Push(second) error = %v", err)
	}

	segmenter.classifier = fakeClassifier{speech: false}
	ended, err := segmenter.Push(context.Background(), testFrame(t, 3, time.Unix(10, 700_000_000)))
	if err != nil {
		t.Fatalf("Push(silence) error = %v", err)
	}
	if len(ended) != 1 || ended[0].Type != EventFinal {
		t.Fatalf("Push(silence) events = %#v, want one final", ended)
	}
	if len(ended[0].Frames) != 2 {
		t.Fatalf("final frames = %d, want 2", len(ended[0].Frames))
	}
	flushed, err := segmenter.Flush(context.Background(), time.Unix(11, 0))
	if err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(flushed) != 0 {
		t.Fatalf("duplicate Flush() events = %#v, want none", flushed)
	}
}

func TestSegmenterFinalizesAtMaximumDurationAndDoesNotDuplicate(t *testing.T) {
	segmenter := newTestSegmenter(t, fakeClassifier{speech: true})
	if _, err := segmenter.Push(context.Background(), testFrame(t, 1, time.Unix(20, 0))); err != nil {
		t.Fatalf("Push(first) error = %v", err)
	}
	events, err := segmenter.Push(context.Background(), testFrame(t, 2, time.Unix(21, 0)))
	if err != nil {
		t.Fatalf("Push(maximum) error = %v", err)
	}
	if len(events) < 1 || events[0].Type != EventFinal {
		t.Fatalf("Push(maximum) events = %#v, want final first", events)
	}
	finals := 0
	for _, event := range events {
		if event.Type == EventFinal {
			finals++
		}
	}
	if finals != 1 {
		t.Fatalf("Push(maximum) final event count = %d, want 1", finals)
	}
}

func TestSegmenterClampsMaximumDurationAfterLongFrameGap(t *testing.T) {
	segmenter := newTestSegmenter(t, fakeClassifier{speech: true})
	startedAt := time.Unix(22, 0)
	if _, err := segmenter.Push(context.Background(), testFrame(t, 1, startedAt)); err != nil {
		t.Fatalf("Push(first) error = %v", err)
	}

	events, err := segmenter.Push(context.Background(), testFrame(t, 2, startedAt.Add(10*time.Second)))
	if err != nil {
		t.Fatalf("Push(after gap) error = %v", err)
	}
	if len(events) < 1 || events[0].Type != EventFinal {
		t.Fatalf("Push(after gap) events = %#v, want final first", events)
	}
	wantEndedAt := startedAt.Add(time.Second)
	if got := events[0].EndedAt; !got.Equal(wantEndedAt) {
		t.Fatalf("maximum final EndedAt = %s, want %s", got, wantEndedAt)
	}
}

func TestSegmenterRejectsNonMonotonicFrames(t *testing.T) {
	segmenter := newTestSegmenter(t, fakeClassifier{speech: true})
	if _, err := segmenter.Push(context.Background(), testFrame(t, 1, time.Unix(30, 0))); err != nil {
		t.Fatalf("Push(first) error = %v", err)
	}
	_, err := segmenter.Push(context.Background(), testFrame(t, 2, time.Unix(29, 0)))
	if !errors.Is(err, ErrNonMonotonicTimestamp) {
		t.Fatalf("Push(non-monotonic) error = %v, want %v", err, ErrNonMonotonicTimestamp)
	}
}

func TestSegmenterRejectsPartialPCMFrame(t *testing.T) {
	segmenter := newTestSegmenter(t, fakeClassifier{speech: true})
	_, err := segmenter.Push(context.Background(), audio.Frame{
		PCM:        []byte{1},
		SampleRate: audio.SupportedSampleRate,
		CapturedAt: time.Unix(35, 0),
	})
	if !errors.Is(err, audio.ErrPCMAlignment) {
		t.Fatalf("Push(partial PCM) error = %v, want %v", err, audio.ErrPCMAlignment)
	}
}

func TestSegmenterCancellationDiscardsActiveUtterance(t *testing.T) {
	segmenter := newTestSegmenter(t, fakeClassifier{speech: true})
	if _, err := segmenter.Push(context.Background(), testFrame(t, 1, time.Unix(40, 0))); err != nil {
		t.Fatalf("Push(first) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := segmenter.Push(ctx, testFrame(t, 2, time.Unix(40, 1))); !errors.Is(err, context.Canceled) {
		t.Fatalf("Push(cancelled) error = %v, want %v", err, context.Canceled)
	}
	if events, err := segmenter.Flush(context.Background(), time.Unix(41, 0)); err != nil || len(events) != 0 {
		t.Fatalf("Flush(after cancellation) = %#v, %v; want no event", events, err)
	}
}

func TestNilSegmenterReturnsDependencyError(t *testing.T) {
	var segmenter *Segmenter
	frame := testFrame(t, 1, time.Unix(50, 0))
	if _, err := segmenter.Push(context.Background(), frame); !errors.Is(err, ErrClassifierRequired) {
		t.Fatalf("nil Push() error = %v, want %v", err, ErrClassifierRequired)
	}
	if _, err := segmenter.Flush(context.Background(), time.Unix(51, 0)); !errors.Is(err, ErrClassifierRequired) {
		t.Fatalf("nil Flush() error = %v, want %v", err, ErrClassifierRequired)
	}
}

func newTestSegmenter(t *testing.T, classifier Classifier) *Segmenter {
	t.Helper()
	segmenter, err := NewSegmenter(classifier, Options{
		SilenceAfter: 500 * time.Millisecond,
		MaxDuration:  time.Second,
	})
	if err != nil {
		t.Fatalf("NewSegmenter() error = %v", err)
	}
	return segmenter
}

func testFrame(t *testing.T, value byte, capturedAt time.Time) audio.Frame {
	t.Helper()
	frame, err := audio.NewFrame([]byte{value, 0}, audio.SupportedSampleRate, capturedAt)
	if err != nil {
		t.Fatalf("audio.NewFrame() error = %v", err)
	}
	return frame
}
