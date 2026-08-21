import { describe, expect, it } from "vitest";

import { parseAssistantReply } from "./assistant-replies";

describe("parseAssistantReply", () => {
  it("accepts the flat realtime event shape", () => {
    expect(
      parseAssistantReply({
        type: "assistant.reply",
        event_id: "assistant_reply_turn-1",
        turn_id: "turn-1",
        text: "你好，我可以帮你。",
        language: "zh-CN",
      }),
    ).toEqual({
      type: "assistant.reply",
      eventId: "assistant_reply_turn-1",
      turnId: "turn-1",
      text: "你好，我可以帮你。",
      language: "zh-CN",
    });
  });

  it("accepts a payload-wrapped event", () => {
    expect(
      parseAssistantReply({
        event: "assistant.reply",
        payload: {
          event_id: "assistant_reply_turn-2",
          turn_id: "turn-2",
          text: "Hello",
          language: "en-US",
        },
      })?.turnId,
    ).toBe("turn-2");
  });

  it("ignores unrelated or empty events", () => {
    expect(
      parseAssistantReply({ type: "translation.final", text: "Hello" }),
    ).toBeNull();
    expect(
      parseAssistantReply({ type: "assistant.reply", text: "  " }),
    ).toBeNull();
  });
});
