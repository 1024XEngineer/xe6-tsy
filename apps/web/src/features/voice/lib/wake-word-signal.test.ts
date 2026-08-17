import { describe, expect, it, vi } from "vitest";

import { sendWakeWordDetectedSignal } from "./wake-word-signal";

function fakeSession(options?: {
  readyState?: RTCDataChannelState;
  send?: (payload: string) => void;
}) {
  return {
    wakeWordChannel: {
      readyState: options?.readyState ?? "open",
      send: options?.send ?? vi.fn(),
    } as unknown as RTCDataChannel,
  };
}

describe("sendWakeWordDetectedSignal", () => {
  it("cancels playback and sends the typed signal without controlling media", () => {
    const steps: string[] = [];
    const send = vi.fn((payload: string) => steps.push(`send:${payload}`));
    const session = fakeSession({
      send,
    });

    const result = sendWakeWordDetectedSignal(session, {
      cancelPlayback: () => steps.push("cancel"),
      createSignalId: () => "wake-1",
      now: () => new Date("2026-08-11T10:00:00.000Z"),
    });

    expect(result).toEqual({
      ok: true,
      signal: {
        type: "wake_word.detected",
        event_version: 1,
        signal_id: "wake-1",
        detected_at: "2026-08-11T10:00:00.000Z",
      },
    });
    expect(steps).toEqual([
      "cancel",
      `send:${JSON.stringify(result.ok ? result.signal : null)}`,
    ]);
  });

  it("keeps the session usable when the DataChannel is not open", () => {
    const cancelPlayback = vi.fn();
    const session = fakeSession({
      readyState: "connecting",
    });

    expect(
      sendWakeWordDetectedSignal(session, { cancelPlayback }),
    ).toEqual({ ok: false, reason: "data_channel_not_open" });
    expect(cancelPlayback).toHaveBeenCalledOnce();
  });

  it("returns a send failure without closing transport handles", () => {
    const error = new Error("buffer full");
    const session = fakeSession({
      send: () => {
        throw error;
      },
    });

    expect(
      sendWakeWordDetectedSignal(session, {
        cancelPlayback: vi.fn(),
        createSignalId: () => "wake-2",
      }),
    ).toEqual({ ok: false, reason: "send_failed", error });
  });
});
