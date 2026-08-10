import { describe, expect, it } from "vitest";

import { DEFAULT_VOICE_CONFIG } from "./languages";
import {
  loadVoiceConfig,
  normalizeVoiceConfig,
  saveVoiceConfig,
} from "./voice-settings";

describe("voice-settings", () => {
  it("keeps a valid zh-CN/en-US pair", () => {
    expect(
      normalizeVoiceConfig({
        sourceLanguage: "zh-CN",
        targetLanguage: "en-US",
        outputMode: "bidirectional",
      }),
    ).toEqual({
      sourceLanguage: "zh-CN",
      targetLanguage: "en-US",
      outputMode: "bidirectional",
    });
  });

  it("repairs identical source and target languages", () => {
    expect(
      normalizeVoiceConfig({
        sourceLanguage: "zh-CN",
        targetLanguage: "zh-CN",
        outputMode: "bidirectional",
      }),
    ).toEqual({
      sourceLanguage: "zh-CN",
      targetLanguage: "en-US",
      outputMode: "bidirectional",
    });
  });

  it("falls back unknown codes to defaults", () => {
    expect(
      normalizeVoiceConfig({
        sourceLanguage: "mandarin" as never,
        targetLanguage: "english" as never,
        outputMode: "bidirectional",
      }),
    ).toEqual(DEFAULT_VOICE_CONFIG);
  });

  it("persists the selected default pair for the next session", () => {
    saveVoiceConfig({
      sourceLanguage: "en-US",
      targetLanguage: "zh-CN",
      outputMode: "bidirectional",
    });

    expect(loadVoiceConfig(DEFAULT_VOICE_CONFIG)).toEqual({
      sourceLanguage: "en-US",
      targetLanguage: "zh-CN",
      outputMode: "bidirectional",
    });
  });

  it("falls back when a saved language is no longer supported", () => {
    expect(
      normalizeVoiceConfig({
        sourceLanguage: "vi-VN",
        targetLanguage: "en-US",
        outputMode: "bidirectional",
      }),
    ).toEqual(DEFAULT_VOICE_CONFIG);
  });

  it("defaults legacy settings to bidirectional output", () => {
    expect(
      normalizeVoiceConfig({
        sourceLanguage: "zh-CN",
        targetLanguage: "en-US",
        outputMode: undefined as never,
      }),
    ).toEqual(DEFAULT_VOICE_CONFIG);
  });
});
