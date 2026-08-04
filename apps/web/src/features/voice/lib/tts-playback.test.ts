import { afterEach, describe, expect, it, vi } from "vitest";

import { enqueueTTSAudio, parseTTSAudioEvent } from "./tts-playback";

class FakeAudioContext {
  state: AudioContextState = "running";
  destination = {} as AudioDestinationNode;

  createBuffer() {
    return {
      getChannelData: () => new Float32Array(1),
    } as unknown as AudioBuffer;
  }

  createBufferSource() {
    const source = {
      buffer: null,
      connect: vi.fn(),
      onended: null as (() => void) | null,
      start() {
        queueMicrotask(() => source.onended?.());
      },
    };
    return source as unknown as AudioBufferSourceNode;
  }

  resume() {
    return Promise.resolve();
  }
}

describe("parseTTSAudioEvent", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("parses pcm_s16le DataChannel payloads", () => {
    const pcm = new Uint8Array([0, 0, 0, 16]);
    const event = parseTTSAudioEvent({
      type: "tts.audio",
      playback_id: "playback_1",
      sample_rate_hz: 24000,
      channels: 1,
      pcm_base64: btoa(String.fromCharCode(...pcm)),
    });
    expect(event?.playbackId).toBe("playback_1");
    expect(event?.sampleRateHz).toBe(24000);
    expect(new Uint8Array(event!.pcm)).toEqual(pcm);
  });

  it("ignores unrelated events", () => {
    expect(parseTTSAudioEvent({ type: "translation.final" })).toBeNull();
  });

  it("treats missing final as a complete single clip", () => {
    const event = parseTTSAudioEvent({
      type: "tts.audio",
      sample_rate_hz: 24000,
      pcm_base64: btoa("abcd"),
    });
    expect(event?.final).toBe(true);
  });

  it("waits when final is explicitly false", () => {
    const event = parseTTSAudioEvent({
      type: "tts.audio",
      sample_rate_hz: 24000,
      sequence: 1,
      final: false,
      pcm_base64: btoa("abcd"),
    });
    expect(event?.final).toBe(false);
    expect(event?.sequence).toBe(1);
  });

  it("notifies when a queued clip starts and ends", async () => {
    vi.stubGlobal("AudioContext", FakeAudioContext);
    const states: boolean[] = [];

    enqueueTTSAudio(
      {
        playbackId: "playback-state",
        sampleRateHz: 24000,
        channels: 1,
        encoding: "pcm_s16le",
        sequence: 1,
        final: true,
        pcm: new Uint8Array([0, 0]).buffer,
      },
      (playing) => states.push(playing),
    );

    await vi.waitFor(() => expect(states).toEqual([true, false]));
  });
});
