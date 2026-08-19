import { describe, expect, it } from "vitest";
import { parseASRPartial, parsePhraseSubtitle, parseTranslationFinal } from "./translation-events";

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

describe("parseASRPartial", () => {
  it("accepts a versioned ephemeral snapshot with an optional source language", () => {
    const event = parseASRPartial({
      type: "asr.partial",
      event_version: 1,
      session_id: "session-1",
      turn_id: "turn-1",
      text: "你好",
      occurred_at: "2026-08-18T01:02:03Z",
    });
    expect(event).toMatchObject({
      sessionId: "session-1",
      turnId: "turn-1",
      text: "你好",
      sourceLanguage: "",
    });
  });

  it("rejects invalid versions and incomplete snapshots", () => {
    expect(parseASRPartial({ type: "asr.partial", event_version: 2 })).toBeNull();
    expect(
      parseASRPartial({
        type: "asr.partial",
        event_version: 1,
        session_id: "session-1",
        turn_id: "turn-1",
        text: "你好",
        occurred_at: "not-a-date",
      }),
    ).toBeNull();
  });
});

describe("parsePhraseSubtitle", () => {
  it("accepts versioned ordered source subtitle phrases", () => {
    const event = parsePhraseSubtitle({
      type: "phrase.subtitle",
      event_version: 1,
      session_id: "session-1",
      utterance_id: "turn-1",
      phrase_sequence: 2,
      source_text: "你好，",
      status: "source_stable",
      occurred_at: "2026-08-19T01:02:03Z",
    });
    expect(event).toMatchObject({
      utteranceId: "turn-1",
      phraseSequence: 2,
      sourceText: "你好，",
      status: "source_stable",
    });
  });

  it("rejects incomplete and unknown subtitle states", () => {
    expect(parsePhraseSubtitle({ type: "phrase.subtitle", event_version: 1 })).toBeNull();
    expect(
      parsePhraseSubtitle({
        type: "phrase.subtitle",
        event_version: 1,
        session_id: "session-1",
        utterance_id: "turn-1",
        phrase_sequence: 1,
        source_text: "你好",
        status: "pending",
        occurred_at: "2026-08-19T01:02:03Z",
      }),
    ).toBeNull();
  });
});
