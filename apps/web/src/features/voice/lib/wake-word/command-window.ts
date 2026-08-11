import type { WakeCommand } from "./keywords";

export const DEFAULT_COMMAND_WINDOW_MS = 5_000;

export type CommandWindowState = "closed" | "open";

export type CommandWindowSnapshot = {
  state: CommandWindowState;
  expiresAt: number | null;
};

export type LocalCommandWindowOptions = {
  durationMs?: number;
  now?: () => number;
  setTimer?: (callback: () => void, delayMs: number) => ReturnType<typeof setTimeout>;
  clearTimer?: (timer: ReturnType<typeof setTimeout>) => void;
};

/**
 * Keeps the browser-side confirmation window bounded without deciding a mode.
 * The server remains the authority for command parsing and mode state; this
 * helper only gates the next already-classified compatible wake command.
 */
export class LocalCommandWindow {
  private readonly durationMs: number;
  private readonly now: () => number;
  private readonly setTimer: (
    callback: () => void,
    delayMs: number,
  ) => ReturnType<typeof setTimeout>;
  private readonly clearTimer: (timer: ReturnType<typeof setTimeout>) => void;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private expiresAt: number | null = null;

  constructor(options: LocalCommandWindowOptions = {}) {
    this.durationMs = options.durationMs ?? DEFAULT_COMMAND_WINDOW_MS;
    this.now = options.now ?? Date.now;
    this.setTimer =
      options.setTimer ?? ((callback, delayMs) => setTimeout(callback, delayMs));
    this.clearTimer = options.clearTimer ?? ((timer) => clearTimeout(timer));
  }

  snapshot(): CommandWindowSnapshot {
    this.expireIfNeeded();
    return {
      state: this.expiresAt === null ? "closed" : "open",
      expiresAt: this.expiresAt,
    };
  }

  open(): CommandWindowSnapshot {
    this.clearTimerIfPresent();
    this.expiresAt = this.now() + this.durationMs;
    this.timer = this.setTimer(() => {
      this.timer = null;
      this.expiresAt = null;
    }, this.durationMs);
    return this.snapshot();
  }

  close(): CommandWindowSnapshot {
    this.clearTimerIfPresent();
    this.expiresAt = null;
    return this.snapshot();
  }

  /** Accept one existing start/stop result, then close the window. */
  consume(command: WakeCommand): boolean {
    if (!this.isOpen()) return false;
    if (command !== "start" && command !== "stop") return false;
    this.close();
    return true;
  }

  dispose(): void {
    this.close();
  }

  private isOpen(): boolean {
    this.expireIfNeeded();
    return this.expiresAt !== null;
  }

  private expireIfNeeded(): void {
    if (this.expiresAt !== null && this.now() >= this.expiresAt) {
      this.close();
    }
  }

  private clearTimerIfPresent(): void {
    if (this.timer === null) return;
    this.clearTimer(this.timer);
    this.timer = null;
  }
}
