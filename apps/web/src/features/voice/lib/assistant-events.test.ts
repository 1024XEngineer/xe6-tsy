import { describe, expect, it } from "vitest";

import { parseAssistantReply } from "./assistant-events";

describe("parseAssistantReply", () => {
  it("normalizes a flat assistant reply event", () => {
    expect(
      parseAssistantReply({
        type: "assistant.reply",
        id: "reply-1",
        turn_id: "turn-1",
        text: "你好，我可以帮你。",
        language: "zh-CN",
      }),
    ).toEqual({
      type: "assistant.reply",
      replyId: "reply-1",
      turnId: "turn-1",
      text: "你好，我可以帮你。",
      language: "zh-CN",
    });
  });

  it("accepts a payload-wrapped event and rejects unrelated messages", () => {
    expect(
      parseAssistantReply({
        event: "assistant.reply",
        payload: { id: "reply-2", text: "Hello", language_code: "en-US" },
      }),
    ).toMatchObject({ replyId: "reply-2", text: "Hello", language: "en-US" });
    expect(parseAssistantReply({ type: "translation.final", text: "Hello" })).toBeNull();
    expect(parseAssistantReply({ type: "assistant.reply", text: " " })).toBeNull();
  });
});
