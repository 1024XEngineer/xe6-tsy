import { describe, expect, it } from "vitest";

import {
  formatActivePair,
  outputRoutes,
  type VoiceSessionConfig,
  voiceConfigFromLanguageConfig,
} from "./languages";

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

  it("formats the active direction for single output", () => {
    expect(formatActivePair(config)).toBe("单向播报 · 中文 → English");
    expect(formatActivePair({ ...config, outputMode: "bidirectional" })).toBe(
      "双向播报 · 中文 ⇄ English",
    );
  });

  it("derives the active output mode from routes returned by the API", () => {
    expect(
      voiceConfigFromLanguageConfig(
        {
          language_pairs: [
            { source: "zh-CN", target: "en-US" },
            { source: "en-US", target: "zh-CN" },
          ],
          output_routes: [
            { target_language: "en-US", tts_enabled: true, delivery_enabled: false },
            { target_language: "zh-CN", tts_enabled: false, delivery_enabled: true },
          ],
        },
        { ...config, outputMode: "bidirectional" },
      ),
    ).toEqual(config);
    expect(
      voiceConfigFromLanguageConfig(
        {
          language_pairs: [
            { source: "zh-CN", target: "en-US" },
            { source: "en-US", target: "zh-CN" },
          ],
          output_routes: [
            { target_language: "en-US", tts_enabled: true, delivery_enabled: false },
            { target_language: "zh-CN", tts_enabled: true, delivery_enabled: false },
          ],
        },
        config,
      ),
    ).toEqual({ ...config, outputMode: "bidirectional" });
  });
});
