import type { MobileState } from "./runtime-client.ts";

export interface ReconnectDecision {
  waitMs: number;
  continue: boolean;
}

export interface ReconnectPolicy {
  next(attempt: number, state: MobileState): ReconnectDecision;
}

export interface Sleep {
  (milliseconds: number): Promise<void>;
}

export class ExponentialReconnectPolicy implements ReconnectPolicy {
  private readonly maxAttempts: number;
  private readonly baseDelayMs: number;
  private readonly maxDelayMs: number;

  constructor(
    maxAttempts = 5,
    baseDelayMs = 500,
    maxDelayMs = 8_000,
  ) {
    this.maxAttempts = maxAttempts;
    this.baseDelayMs = baseDelayMs;
    this.maxDelayMs = maxDelayMs;
  }

  next(attempt: number): ReconnectDecision {
    if (attempt > this.maxAttempts) return { waitMs: 0, continue: false };
    const waitMs = Math.min(this.maxDelayMs, this.baseDelayMs * 2 ** (attempt - 1));
    return { waitMs, continue: true };
  }
}

export const realSleep: Sleep = (milliseconds) =>
  new Promise((resolve) => setTimeout(resolve, milliseconds));
