import { describe, expect, it } from "vitest";

import {
  parsePlaybackLifecycleEvent,
  PlaybackLifecycleTracker,
  type PlaybackLifecycleEvent,
  type PlaybackLifecycleEventType,
} from "./playback-events";

function lifecycleEvent(
  type: PlaybackLifecycleEventType,
  playbackId: string,
): PlaybackLifecycleEvent {
  return {
    type,
    eventId: `${playbackId}-${type}`,
    sessionId: "vs-1",
    turnId: "turn-1",
    playbackId,
    sequenceNo: type === "playback.started" ? 1 : 2,
    reason: "",
    occurredAt: "2026-08-25T12:34:56Z",
  };
}

describe("parsePlaybackLifecycleEvent", () => {
  it("parses the flat realtime-audio playback contract", () => {
    expect(
      parsePlaybackLifecycleEvent({
        event_id: "p1_playback.started_1",
        type: "playback.started",
        session_id: "vs-1",
        turn_id: "turn-1",
        playback_id: "p1",
        sequence_no: 1,
        occurred_at: "2026-08-25T12:34:56.123Z",
      }),
    ).toEqual({
      type: "playback.started",
      eventId: "p1_playback.started_1",
      sessionId: "vs-1",
      turnId: "turn-1",
      playbackId: "p1",
      sequenceNo: 1,
      reason: "",
      occurredAt: "2026-08-25T12:34:56.123Z",
    });
  });

  it("accepts payload-wrapped camelCase lifecycle events", () => {
    expect(
      parsePlaybackLifecycleEvent({
        type: "event",
        eventId: "p1_playback.cancelled_2",
        payload: {
          event: "playback.cancelled",
          sessionId: "vs-1",
          turnId: "turn-1",
          playbackId: "p1",
          sequenceNo: 2,
          reason: "tts_stream_failed",
          occurredAt: "2026-08-25T12:34:58Z",
        },
      }),
    ).toMatchObject({
      type: "playback.cancelled",
      eventId: "p1_playback.cancelled_2",
      sessionId: "vs-1",
      playbackId: "p1",
      sequenceNo: 2,
      reason: "tts_stream_failed",
    });
  });

  it("rejects unrelated and malformed lifecycle messages", () => {
    expect(parsePlaybackLifecycleEvent({ type: "translation.final" })).toBeNull();
    expect(
      parsePlaybackLifecycleEvent({
        event_id: "bad",
        type: "playback.started",
        session_id: "vs-1",
        playback_id: "p1",
        sequence_no: 0,
        occurred_at: "not-a-date",
      }),
    ).toBeNull();
  });
});

describe("PlaybackLifecycleTracker", () => {
  it("keeps playing until every active playback ID settles", () => {
    const tracker = new PlaybackLifecycleTracker();

    expect(tracker.apply(lifecycleEvent("playback.started", "p1"))).toEqual({
      changed: true,
      playing: true,
    });
    tracker.apply(lifecycleEvent("playback.started", "p2"));
    expect(tracker.apply(lifecycleEvent("playback.finished", "p1"))).toEqual({
      changed: true,
      playing: true,
    });
    expect(tracker.apply(lifecycleEvent("playback.interrupted", "p2"))).toEqual({
      changed: true,
      playing: false,
    });
  });

  it("does not let unknown terminals clear active playback or late starts revive", () => {
    const tracker = new PlaybackLifecycleTracker();
    tracker.apply(lifecycleEvent("playback.started", "current"));

    expect(tracker.apply(lifecycleEvent("playback.cancelled", "late"))).toEqual({
      changed: false,
      playing: true,
    });
    expect(tracker.apply(lifecycleEvent("playback.started", "late"))).toEqual({
      changed: false,
      playing: true,
    });
    expect(tracker.apply(lifecycleEvent("playback.finished", "current")).playing).toBe(false);
  });

  it("clears active and retired playback IDs on reset", () => {
    const tracker = new PlaybackLifecycleTracker();
    tracker.apply(lifecycleEvent("playback.finished", "p1"));
    tracker.reset();

    expect(tracker.apply(lifecycleEvent("playback.started", "p1"))).toEqual({
      changed: true,
      playing: true,
    });
  });
});
