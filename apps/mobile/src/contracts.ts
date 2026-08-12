/**
 * Type-only projection of packages/contracts/openapi.yaml. The repository does
 * not currently publish a TypeScript generator or generated package, so this
 * boundary must be replaced by generated types before adding new fields.
 */
export type Mode = "assistant" | "interpretation";

export type ModePhase = "active" | "switching";

export type RuntimeState =
  | "stopped"
  | "starting"
  | "listening"
  | "asr_processing"
  | "translating"
  | "thinking"
  | "tts_processing"
  | "playing"
  | "stopping"
  | "failed";

export type ConnectionState =
  | "new"
  | "connecting"
  | "connected"
  | "disconnected"
  | "failed"
  | "closed";

export type ModeSwitchStatus = "applied" | "unchanged";

export interface RuntimeSnapshot {
  session_id: string;
  start_operation_id: string;
  runtime_state: RuntimeState;
  current_turn_id: string | null;
  current_playback_id: string | null;
  last_error_code: string | null;
  updated_at: string;
}

export interface ModeStateSnapshot {
  session_id: string;
  runtime_instance_id: string;
  active_mode: Mode;
  generation: number;
  phase: ModePhase;
  last_operation_id: string | null;
  updated_at: string;
}

export interface ConnectionSnapshot {
  session_id: string;
  connection_id: string;
  state: ConnectionState;
  version: number;
  updated_at: string;
}

export interface SwitchModeCommand {
  session_id: string;
  runtime_instance_id: string;
  operation_id: string;
  trace_id: string;
  expected_generation: number;
  target_mode: Mode;
}

export interface SwitchModeResult {
  operation_id: string;
  status: ModeSwitchStatus;
  state: ModeStateSnapshot;
}

export const DEFAULT_MODE: Mode = "interpretation";

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
  return snapshot?.active_mode ?? DEFAULT_MODE;
}
