import { describe, expect, it, vi } from "vitest";

import { LocalCommandWindow } from "./command-window";

describe("LocalCommandWindow", () => {
  it("opens for five seconds and consumes one compatible command", () => {
    const now = 10_000;
    const window = new LocalCommandWindow({ now: () => now });

    expect(window.snapshot()).toEqual({ state: "closed", expiresAt: null });
    expect(window.open()).toEqual({ state: "open", expiresAt: 15_000 });
    expect(window.consume("start")).toBe(true);
    expect(window.snapshot()).toEqual({ state: "closed", expiresAt: null });
    expect(window.consume("stop")).toBe(false);
  });

  it("expires without accepting a command after the deadline", () => {
    let now = 0;
    const window = new LocalCommandWindow({ durationMs: 5000, now: () => now });

    window.open();
    now = 5000;

    expect(window.snapshot()).toEqual({ state: "closed", expiresAt: null });
    expect(window.consume("stop")).toBe(false);
  });

  it("clears the previous timer when reopening or closing", () => {
    const timers: Array<() => void> = [];
    const clearTimer = vi.fn();
    const window = new LocalCommandWindow({
      now: () => 0,
      setTimer: (callback) => {
        timers.push(callback);
        return timers.length as unknown as ReturnType<typeof setTimeout>;
      },
      clearTimer,
    });

    window.open();
    window.open();
    window.close();

    expect(clearTimer).toHaveBeenCalledTimes(2);
  });
});
