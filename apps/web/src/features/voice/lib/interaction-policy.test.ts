import { describe, expect, it, vi } from "vitest";

import {
  effectiveVoiceInteractionPolicy,
  loadVoiceInteractionPolicy,
  saveVoiceInteractionPolicy,
  shouldSuppressMicrophoneDuringTTS,
} from "./interaction-policy";

describe("voice interaction policy", () => {
  it("forces continuous uplink only while interpretation is active", () => {
    expect(effectiveVoiceInteractionPolicy("interpretation", "wake_word")).toBe(
      "continuous",
    );
    expect(effectiveVoiceInteractionPolicy("assistant", "wake_word")).toBe(
      "wake_word",
    );
  });

  it("keeps capture active while interpretation audio is playing", () => {
    expect(shouldSuppressMicrophoneDuringTTS("interpretation")).toBe(false);
    expect(shouldSuppressMicrophoneDuringTTS("assistant")).toBe(true);
  });

  it("defaults missing, unknown, and blocked storage to continuous listening", () => {
    expect(loadVoiceInteractionPolicy(undefined)).toBe("continuous");
    expect(loadVoiceInteractionPolicy({ getItem: () => "other" })).toBe(
      "continuous",
    );
    expect(
      loadVoiceInteractionPolicy({
        getItem: () => {
          throw new Error("storage blocked");
        },
      }),
    ).toBe("continuous");
  });

  it("loads and persists either explicit policy", () => {
    expect(loadVoiceInteractionPolicy({ getItem: () => "wake_word" })).toBe(
      "wake_word",
    );
    const setItem = vi.fn();
    saveVoiceInteractionPolicy("wake_word", { setItem });
    saveVoiceInteractionPolicy("continuous", { setItem });
    expect(setItem).toHaveBeenNthCalledWith(
      1,
      "lingow.voice.interaction-policy",
      "wake_word",
    );
    expect(setItem).toHaveBeenNthCalledWith(
      2,
      "lingow.voice.interaction-policy",
      "continuous",
    );
  });

  it("keeps the in-memory choice when persistence throws", () => {
    expect(() =>
      saveVoiceInteractionPolicy("wake_word", {
        setItem: () => {
          throw new Error("storage blocked");
        },
      }),
    ).not.toThrow();
  });
});
