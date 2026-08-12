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

  it("notifies the owner when the bounded timer expires", () => {
    let expire: (() => void) | null = null;
    const onExpire = vi.fn();
    const window = new LocalCommandWindow({
      onExpire,
      setTimer: (callback) => {
        expire = callback;
        return 1 as unknown as ReturnType<typeof setTimeout>;
      },
    });

    window.open();
    const fireExpire = expire as (() => void) | null;
    expect(fireExpire).not.toBeNull();
    fireExpire?.();

    expect(onExpire).toHaveBeenCalledTimes(1);
    expect(window.snapshot()).toEqual({ state: "closed", expiresAt: null });
  });

  it("ignores a queued expiry callback from an older window", () => {
    const timers: Array<() => void> = [];
    const onExpire = vi.fn();
    const window = new LocalCommandWindow({
      onExpire,
      setTimer: (callback) => {
        timers.push(callback);
        return timers.length as unknown as ReturnType<typeof setTimeout>;
      },
    });

    window.open();
    window.open();
    timers[0]?.();

    expect(onExpire).not.toHaveBeenCalled();
    expect(window.snapshot().state).toBe("open");
    timers[1]?.();
    expect(onExpire).toHaveBeenCalledTimes(1);
    expect(window.snapshot().state).toBe("closed");
  });
});
