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
      }),
    ).toEqual({
      sourceLanguage: "zh-CN",
      targetLanguage: "en-US",
    });
  });

  it("repairs identical source and target languages", () => {
    expect(
      normalizeVoiceConfig({
        sourceLanguage: "zh-CN",
        targetLanguage: "zh-CN",
      }),
    ).toEqual({
      sourceLanguage: "zh-CN",
      targetLanguage: "en-US",
    });
  });

  it("falls back unknown codes to defaults", () => {
    expect(
      normalizeVoiceConfig({
        sourceLanguage: "mandarin" as never,
        targetLanguage: "english" as never,
      }),
    ).toEqual(DEFAULT_VOICE_CONFIG);
  });

  it("persists the selected default pair for the next session", () => {
    saveVoiceConfig({ sourceLanguage: "en-US", targetLanguage: "zh-CN" });

    expect(loadVoiceConfig(DEFAULT_VOICE_CONFIG)).toEqual({
      sourceLanguage: "en-US",
      targetLanguage: "zh-CN",
    });
  });

  it("falls back when a saved language is no longer supported", () => {
    expect(
      normalizeVoiceConfig({
        sourceLanguage: "vi-VN",
        targetLanguage: "en-US",
      }),
    ).toEqual(DEFAULT_VOICE_CONFIG);
  });
});
