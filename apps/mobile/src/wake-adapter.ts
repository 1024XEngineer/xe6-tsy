/**
 * Adapter boundaries only. This package does not ship a wake-word engine or
 * command-window protocol until the native engine and realtime uplink contract
 * are selected. A host may implement these interfaces with a local engine.
 */
export type WakeWordEvent = { keyword: string; detectedAt: string };

export interface WakeWordEngine {
  start(onWake: (event: WakeWordEvent) => void): Promise<void>;
  stop(): Promise<void>;
}

export type CommandWindowState = "closed" | "open" | "expired";

export interface CommandWindow {
  readonly state: CommandWindowState;
  open(): void;
  close(): void;
}

export function createUnsupportedWakeWordEngine(): WakeWordEngine {
  return {
    async start() {
      throw new Error("local wake-word engine is not configured");
    },
    async stop() {},
  };
}
