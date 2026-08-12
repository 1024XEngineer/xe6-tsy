import { describe, expect, it } from "vitest";

import {
  WAKE_LISTEN_KEYWORD,
  WAKE_START_KEYWORD,
  WAKE_STOP_KEYWORD,
  WAKE_TRIGGERS,
  classifyWakeKeyword,
  resolveWakeTrigger,
} from "./keywords";

describe("WAKE_TRIGGERS catalog", () => {
  it("exposes start, stop, and listen aliases from the registry", () => {
    expect(WAKE_START_KEYWORD).toBe("小灵，开始翻译");
    expect(WAKE_STOP_KEYWORD).toBe("小灵，停止翻译");
    expect(WAKE_LISTEN_KEYWORD).toBe("小灵小灵");
    expect(WAKE_TRIGGERS.map((t) => t.command)).toEqual([
      "start",
      "stop",
      "listen",
    ]);
  });
});

describe("classifyWakeKeyword", () => {
  it("maps catalog display names to commands", () => {
    expect(classifyWakeKeyword("小灵，开始翻译")).toBe("start");
    expect(classifyWakeKeyword("小灵，开始对话")).toBe("start");
    expect(classifyWakeKeyword("小灵，停止翻译")).toBe("stop");
    expect(classifyWakeKeyword("小灵，停止对话")).toBe("stop");
    expect(classifyWakeKeyword("小灵小灵")).toBe("listen");
  });

  it("maps 小林 nasal-final aliases to the same commands and canonical labels", () => {
    expect(classifyWakeKeyword("小林，开始翻译")).toBe("start");
    expect(classifyWakeKeyword("小林，停止翻译")).toBe("stop");
    expect(classifyWakeKeyword("小林小林")).toBe("listen");
    expect(resolveWakeTrigger("小林小林")?.label).toBe("小灵小灵");
    expect(resolveWakeTrigger("小林，开始翻译")?.label).toBe("小灵，开始翻译");
  });

  it("prefers longer labels over the attention trigger", () => {
    expect(classifyWakeKeyword("请说小灵，开始翻译")).toBe("start");
    expect(classifyWakeKeyword("请说小林，停止翻译")).toBe("stop");
    expect(resolveWakeTrigger("小灵，开始翻译")?.id).toBe("start_translate");
  });

  it("trims whitespace before matching", () => {
    expect(classifyWakeKeyword("  小灵，开始翻译  ")).toBe("start");
    expect(classifyWakeKeyword("  小灵小灵  ")).toBe("listen");
  });

  it("returns null for unrelated or single-wake text", () => {
    expect(classifyWakeKeyword("")).toBeNull();
    expect(classifyWakeKeyword("小灵")).toBeNull();
    expect(classifyWakeKeyword("小林")).toBeNull();
    expect(classifyWakeKeyword("开始后停止翻译")).toBeNull();
    expect(classifyWakeKeyword("你好")).toBeNull();
  });
});
