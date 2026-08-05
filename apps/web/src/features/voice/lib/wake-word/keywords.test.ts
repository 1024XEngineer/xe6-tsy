import { describe, expect, it } from "vitest";

import { classifyWakeKeyword } from "./keywords";

describe("classifyWakeKeyword", () => {
  it("maps start and stop display names", () => {
    expect(classifyWakeKeyword("小灵，开始翻译")).toBe("start");
    expect(classifyWakeKeyword("小灵，停止翻译")).toBe("stop");
  });

  it("returns null for unrelated text", () => {
    expect(classifyWakeKeyword("")).toBeNull();
    expect(classifyWakeKeyword("小灵")).toBeNull();
  });
});
