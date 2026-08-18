import { describe, expect, it } from "vitest";

import {
  WAKE_LISTEN_KEYWORD,
  WAKE_TRIGGERS,
  resolveWakeTrigger,
} from "./keywords";

describe("WAKE_TRIGGERS catalog", () => {
  it("exposes only the fixed attention phrase", () => {
    expect(WAKE_LISTEN_KEYWORD).toBe("小灵小灵");
    expect(WAKE_TRIGGERS).toHaveLength(1);
  });
});

describe("resolveWakeTrigger", () => {
  it("trims whitespace before matching", () => {
    expect(resolveWakeTrigger("  小灵小灵  ")?.id).toBe("attention");
  });

  it("rejects aliases and does not classify business commands locally", () => {
    expect(resolveWakeTrigger("")).toBeNull();
    expect(resolveWakeTrigger("小灵")).toBeNull();
    expect(resolveWakeTrigger("小林小林")).toBeNull();
    expect(resolveWakeTrigger("小灵小灵开始翻译")).toBeNull();
    expect(resolveWakeTrigger("小灵，开始翻译")).toBeNull();
    expect(resolveWakeTrigger("小灵，停止翻译")).toBeNull();
    expect(resolveWakeTrigger("你好")).toBeNull();
  });
});
