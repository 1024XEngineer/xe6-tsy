import { describe, expect, it, vi } from "vitest";

import {
  effectiveVoiceInteractionPolicy,
  loadVoiceInteractionPolicy,
  saveVoiceInteractionPolicy,
  shouldSuppressMicrophoneDuringTTS,
} from "./interaction-policy";

describe("voice interaction policy", () => {
  it("uses wake-word capture for assistant and continuous capture for interpretation", () => {
    expect(effectiveVoiceInteractionPolicy("assistant", "continuous")).toBe("wake_word");
    expect(effectiveVoiceInteractionPolicy("assistant", "wake_word")).toBe("wake_word");
    expect(effectiveVoiceInteractionPolicy("interpretation", "continuous")).toBe("continuous");
    expect(effectiveVoiceInteractionPolicy("interpretation", "wake_word")).toBe("continuous");
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
