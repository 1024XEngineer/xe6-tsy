import { describe, expect, it } from "vitest";

import { parseTTSAudioEvent } from "./tts-playback";

describe("parseTTSAudioEvent", () => {
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
});
