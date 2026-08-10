import { describe, expect, it } from "vitest";

import { outputRoutes, type VoiceSessionConfig } from "./languages";

const config: VoiceSessionConfig = {
  sourceLanguage: "zh-CN",
  targetLanguage: "en-US",
  outputMode: "single",
};

describe("outputRoutes", () => {
  it("plays the selected target and delivers the reverse translation in single mode", () => {
    expect(outputRoutes(config)).toEqual([
      {
        target_language: "en-US",
        tts_enabled: true,
        delivery_enabled: false,
      },
      {
        target_language: "zh-CN",
        tts_enabled: false,
        delivery_enabled: true,
      },
    ]);
  });

  it("enables TTS for both targets in bidirectional mode", () => {
    expect(outputRoutes({ ...config, outputMode: "bidirectional" })).toEqual([
      {
        target_language: "en-US",
        tts_enabled: true,
        delivery_enabled: false,
      },
      {
        target_language: "zh-CN",
        tts_enabled: true,
        delivery_enabled: false,
      },
    ]);
  });
});
