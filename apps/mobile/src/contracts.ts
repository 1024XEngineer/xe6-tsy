export type {
  ModePhase,
  ModeStateSnapshot,
  ModeSwitchStatus,
  RealtimeConnectionSnapshot as ConnectionSnapshot,
  RealtimeConnectionState as ConnectionState,
  RealtimeMode as Mode,
  RealtimeRuntimeSnapshot as RuntimeSnapshot,
  RealtimeRuntimeState as RuntimeState,
  SwitchModeCommand,
  SwitchModeResult,
  VoiceSession,
  VoiceSessionAudioConfig,
  VoiceSessionCapabilities,
  VoiceSessionStatus,
} from "../../../packages/contracts/typescript/realtime.ts";

import type {
  ModePhase,
  ModeSwitchStatus,
  RealtimeConnectionState as ConnectionState,
  ModeStateSnapshot,
  RealtimeMode as Mode,
  RealtimeRuntimeState as RuntimeState,
} from "../../../packages/contracts/typescript/realtime.ts";

/** New assistant-capable clients explicitly request this mode at Start. */
export const DEFAULT_INITIAL_MODE: Mode = "assistant";
/** Only missing mode state from an explicitly recognized legacy server uses this. */
export const LEGACY_MODE_FALLBACK: Mode = "interpretation";

export function isMode(value: unknown): value is Mode {
  return value === "assistant" || value === "interpretation";
}

export function isConnectionState(value: unknown): value is ConnectionState {
  return (
    value === "new" ||
    value === "connecting" ||
    value === "connected" ||
    value === "disconnected" ||
    value === "failed" ||
    value === "closed"
  );
}

export function isRuntimeState(value: unknown): value is RuntimeState {
  return (
    value === "stopped" ||
    value === "starting" ||
    value === "listening" ||
    value === "asr_processing" ||
    value === "translating" ||
    value === "thinking" ||
    value === "assistant_processing" ||
    value === "tts_processing" ||
    value === "playing" ||
    value === "stopping" ||
    value === "failed"
  );
}

export function isModePhase(value: unknown): value is ModePhase {
  return value === "active" || value === "switching";
}

export function isModeSwitchStatus(value: unknown): value is ModeSwitchStatus {
  return value === "applied" || value === "unchanged";
}

export function effectiveMode(snapshot: ModeStateSnapshot | null): Mode {
  // A missing mode snapshot is the rolling-compatibility path for old clients.
  // It must not block the existing interpretation flow or invent a mode state.
  return snapshot?.active_mode ?? LEGACY_MODE_FALLBACK;
}
