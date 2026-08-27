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

  it("settles only active containers whose final Turn is returned by polling", () => {
    const finalTurn = {
      id: "turn-1",
      sourceLanguage: "中文",
      targetLanguage: "English",
      source: "你好",
      translation: "Hello",
    };
    const live = {
      ...initialSession,
      phase: "active" as const,
      asrPartial: {
        turnId: "turn-1",
        text: "你好",
        sourceLanguage: "zh-CN",
      },
      phraseSubtitles: [
        {
          utteranceId: "turn-1",
          phraseSequence: 1,
          sourceText: "你好",
          translatedText: "Hello",
          status: "translated" as const,
        },
        {
          utteranceId: "turn-2",
          phraseSequence: 1,
          sourceText: "下一句",
          translatedText: "",
          status: "source_stable" as const,
        },
      ],
    };

    const emptyPoll = sessionReducer(live, { type: "SET_TURNS", turns: [] });
    expect(emptyPoll.asrPartial).toEqual(live.asrPartial);
    expect(emptyPoll.phraseSubtitles).toEqual(live.phraseSubtitles);

    const settled = sessionReducer(emptyPoll, {
      type: "SET_TURNS",
      turns: [finalTurn],
    });
    expect(settled.asrPartial).toBeNull();
    expect(settled.phraseSubtitles).toEqual([live.phraseSubtitles[1]]);

    const duplicateFinal = sessionReducer(
      {
        ...live,
        turns: [finalTurn],
        phraseSubtitles: [live.phraseSubtitles[0]],
      },
      { type: "ADD_TURN", turn: finalTurn },
    );
    expect(duplicateFinal.asrPartial).toBeNull();
    expect(duplicateFinal.phraseSubtitles).toEqual([]);
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

  it("keeps assistant replies separate from translation turns", () => {
    const reply = {
      replyId: "reply-1",
      turnId: "turn-1",
      text: "我可以帮你查找路线。",
      language: "zh-CN",
    };
    const withReply = sessionReducer(
      { ...initialSession, phase: "active" },
      { type: "ADD_ASSISTANT_REPLY", reply },
    );
    const duplicate = sessionReducer(withReply, {
      type: "ADD_ASSISTANT_REPLY",
      reply,
    });

    expect(withReply.assistantReplies).toEqual([reply]);
    expect(withReply.turns).toEqual([]);
    expect(duplicate.assistantReplies).toHaveLength(1);
  });

  it("replaces partial text and clears it when the matching final settles", () => {
    const partial = sessionReducer(initialSession, {
      type: "SET_ASR_PARTIAL",
      partial: { turnId: "turn-1", text: "你好", stash: "，请", sourceLanguage: "zh-CN" },
    });
    const replaced = sessionReducer(partial, {
      type: "SET_ASR_PARTIAL",
      partial: { turnId: "turn-1", text: "你好，请问", stash: "您好吗？", sourceLanguage: "zh-CN" },
    });
    const settled = sessionReducer(replaced, {
      type: "ADD_TURN",
      turn: {
        id: "turn-1",
        sourceLanguage: "中文",
        targetLanguage: "English",
        source: "你好，请问",
        translation: "Hello",
      },
    });
    const latePartial = sessionReducer(settled, {
      type: "SET_ASR_PARTIAL",
      partial: { turnId: "turn-1", text: "迟到文本", sourceLanguage: "zh-CN" },
    });

    expect(replaced.asrPartial?.text).toBe("你好，请问");
    expect(replaced.asrPartial?.stash).toBe("您好吗？");
    expect(settled.asrPartial).toBeNull();
    expect(latePartial.asrPartial).toBeNull();
  });

  it("clears matching transient subtitles when polling or duplicate final turns settle", () => {
    const finalTurn = {
      id: "turn-1",
      sourceLanguage: "中文",
      targetLanguage: "English",
      source: "你好，请问",
      translation: "Hello",
    };
    const live = sessionReducer(
      sessionReducer(initialSession, {
        type: "SET_ASR_PARTIAL",
        partial: { turnId: "turn-1", text: "你好", sourceLanguage: "zh-CN" },
      }),
      {
        type: "ADD_PHRASE_SUBTITLE",
        subtitle: {
          utteranceId: "turn-1",
          phraseSequence: 1,
          sourceText: "你好，",
          translatedText: "Hello,",
          status: "translated",
        },
      },
    );

    const polled = sessionReducer(live, { type: "SET_TURNS", turns: [finalTurn] });
    expect(polled.asrPartial).toBeNull();
    expect(polled.phraseSubtitles).toEqual([]);

    const duplicate = sessionReducer(
      { ...live, turns: [finalTurn] },
      { type: "ADD_TURN", turn: finalTurn },
    );
    expect(duplicate.asrPartial).toBeNull();
    expect(duplicate.phraseSubtitles).toEqual([]);
  });

  it("keeps stable phrase subtitles in order and clears them after the final", () => {
    const first = sessionReducer(initialSession, {
      type: "ADD_PHRASE_SUBTITLE",
      subtitle: { utteranceId: "turn-1", phraseSequence: 2, sourceText: "世界", translatedText: "", status: "source_stable" },
    });
    const ordered = sessionReducer(first, {
      type: "ADD_PHRASE_SUBTITLE",
      subtitle: { utteranceId: "turn-1", phraseSequence: 1, sourceText: "你好，", translatedText: "", status: "source_stable" },
    });
    const duplicate = sessionReducer(ordered, {
      type: "ADD_PHRASE_SUBTITLE",
      subtitle: { utteranceId: "turn-1", phraseSequence: 1, sourceText: "你好，", translatedText: "Hello,", status: "translated" },
    });
    const settled = sessionReducer(duplicate, {
      type: "ADD_TURN",
      turn: {
        id: "turn-1",
        sourceLanguage: "中文",
        targetLanguage: "English",
        source: "你好，世界",
        translation: "Hello, world",
      },
    });

    expect(ordered.phraseSubtitles.map((subtitle) => subtitle.sourceText)).toEqual(["你好，", "世界"]);
    expect(duplicate.phraseSubtitles).toEqual([
      { utteranceId: "turn-1", phraseSequence: 1, sourceText: "你好，", translatedText: "Hello,", status: "translated" },
      { utteranceId: "turn-1", phraseSequence: 2, sourceText: "世界", translatedText: "", status: "source_stable" },
    ]);
    expect(settled.phraseSubtitles).toEqual([]);
  });

  it("does not let final content events hide active browser playback", () => {
    const playing = { ...initialSession, phase: "playing" as const };
    const withTurn = sessionReducer(playing, {
      type: "ADD_TURN",
      turn: {
        id: "turn-playing",
        sourceLanguage: "中文",
        targetLanguage: "English",
        source: "你好",
        translation: "Hello",
      },
    });
    const withReply = sessionReducer(playing, {
      type: "ADD_ASSISTANT_REPLY",
      reply: {
        replyId: "reply-playing",
        turnId: "turn-playing",
        text: "Hello",
        language: "en-US",
      },
    });

    expect(withTurn.phase).toBe("playing");
    expect(withReply.phase).toBe("playing");
  });

  it("does not let late source events downgrade a terminal phrase", () => {
    const translated = sessionReducer(initialSession, {
      type: "ADD_PHRASE_SUBTITLE",
      subtitle: { utteranceId: "turn-1", phraseSequence: 1, sourceText: "你好", translatedText: "Hello", status: "translated" },
    });
    const lateSource = sessionReducer(translated, {
      type: "ADD_PHRASE_SUBTITLE",
      subtitle: { utteranceId: "turn-1", phraseSequence: 1, sourceText: "你好", translatedText: "", status: "source_stable" },
    });
    const failed = sessionReducer(initialSession, {
      type: "ADD_PHRASE_SUBTITLE",
      subtitle: { utteranceId: "turn-2", phraseSequence: 1, sourceText: "再见", translatedText: "", status: "translation_failed" },
    });
    const lateFailedSource = sessionReducer(failed, {
      type: "ADD_PHRASE_SUBTITLE",
      subtitle: { utteranceId: "turn-2", phraseSequence: 1, sourceText: "再见", translatedText: "", status: "source_stable" },
    });

    expect(lateSource).toBe(translated);
    expect(lateFailedSource).toBe(failed);
  });

  it("clears transient subtitles when the session falls back or errors", () => {
    const active = {
      ...initialSession,
      phase: "active" as const,
      asrPartial: { turnId: "turn-1", text: "你好", sourceLanguage: "zh-CN" },
      phraseSubtitles: [
        { utteranceId: "turn-1", phraseSequence: 1, sourceText: "你好，", translatedText: "", status: "source_stable" as const },
      ],
    };
    const fallback = sessionReducer(active, {
      type: "FALLBACK",
      message: "切换到模拟输入",
    });
    const errored = sessionReducer(active, { type: "ERROR", message: "连接中断" });

    expect(fallback.asrPartial).toBeNull();
    expect(fallback.phraseSubtitles).toEqual([]);
    expect(errored.asrPartial).toBeNull();
    expect(errored.phraseSubtitles).toEqual([]);
  });
});
