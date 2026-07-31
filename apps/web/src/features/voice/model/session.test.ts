import { describe, expect, it } from "vitest";

import { initialSession, sessionReducer } from "./session";

describe("sessionReducer", () => {
  it("moves from idle to an active bilingual session", () => {
    const listening = sessionReducer(initialSession, { type: "START" });
    const active = sessionReducer(listening, { type: "ACTIVATE" });

    expect(listening.phase).toBe("listening");
    expect(active.phase).toBe("active");
  });

  it("merges polled turns without wiping DataChannel subtitles", () => {
    const first = {
      id: "turn-1",
      sourceLanguage: "中文",
      targetLanguage: "English",
      source: "你好",
      translation: "Hello",
    };
    const second = {
      id: "turn-2",
      sourceLanguage: "English",
      targetLanguage: "中文",
      source: "Hi",
      translation: "嗨",
    };

    const fromDc = sessionReducer(
      { ...initialSession, phase: "active" },
      { type: "ADD_TURN", turn: first },
    );
    const emptyPoll = sessionReducer(fromDc, { type: "SET_TURNS", turns: [] });
    expect(emptyPoll.turns).toEqual([first]);

    const merged = sessionReducer(emptyPoll, {
      type: "SET_TURNS",
      turns: [first, second],
    });
    const deduped = sessionReducer(merged, { type: "ADD_TURN", turn: first });

    expect(merged.turns).toEqual([first, second]);
    expect(deduped.turns).toHaveLength(2);
  });

  it("returns to a clean idle state when the session ends", () => {
    const active = {
      ...initialSession,
      phase: "active" as const,
      turns: [
        {
          id: "turn-1",
          sourceLanguage: "中文",
          targetLanguage: "EN",
          source: "你好",
          translation: "Hello",
        },
      ],
    };

    expect(sessionReducer(active, { type: "END" })).toEqual(initialSession);
  });
});
