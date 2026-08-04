import { describe, expect, it } from "vitest";
import { parseTranslationFinal } from "./translation-events";

describe("parseTranslationFinal", () => {
  it("accepts flat demo events", () => {
    const event = parseTranslationFinal({
      type: "translation.final",
      turn_id: "turn_1",
      source_text: "你好",
      translated_text: "Hello",
      source_language: "zh-CN",
      target_language: "en-US",
    });
    expect(event).toMatchObject({
      type: "translation.final",
      turnId: "turn_1",
      sourceText: "你好",
      translatedText: "Hello",
    });
  });

  it("accepts event + payload shape from contracts", () => {
    const event = parseTranslationFinal({
      event: "translation.final",
      payload: {
        source_text: "你好",
        translated_text: "Hello",
        source_language: "zh-CN",
        target_language: "en-US",
      },
      turn_id: "turn_2",
    });
    expect(event?.turnId).toBe("turn_2");
    expect(event?.translatedText).toBe("Hello");
  });

  it("ignores unrelated events", () => {
    expect(parseTranslationFinal({ type: "asr.partial", text: "x" })).toBeNull();
  });
});
