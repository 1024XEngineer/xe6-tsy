/**
 * Public TypeScript contracts exported from packages/contracts/openapi.yaml.
 * Keep this language binding aligned with the OpenAPI and Go contract tests.
 */
export type RealtimeMode = "assistant" | "interpretation";

export type ModePhase = "active" | "switching";

export type RealtimeRuntimeState =
  | "stopped"
  | "starting"
  | "listening"
  | "asr_processing"
  | "translating"
  | "thinking"
  | "assistant_processing"
  | "tts_processing"
  | "playing"
  | "stopping"
  | "failed";

export type RealtimeConnectionState =
  | "new"
  | "connecting"
  | "connected"
  | "disconnected"
  | "failed"
  | "closed";

export type ModeSwitchStatus = "applied" | "unchanged";

export type CommandResultStatus =
  | "applied"
  | "unchanged"
  | "clarification_required"
  | "unsupported"
  | "failed";

export interface WakeWordDetectedSignal {
  type: "wake_word.detected";
  event_version: 1;
  signal_id: string;
  detected_at: string;
}

export interface CommandResultEvent {
  type: "command.result";
  event_version: 1;
  command_id: string;
  session_id: string;
  runtime_instance_id?: string;
  generation?: number;
  status: CommandResultStatus;
  action?: string;
  target_mode?: RealtimeMode;
  message: string;
  occurred_at: string;
}

export interface RealtimeRuntimeSnapshot {
  session_id: string;
  start_operation_id: string;
  runtime_state: RealtimeRuntimeState;
  current_turn_id: string | null;
  current_playback_id: string | null;
  last_error_code: string | null;
  updated_at: string;
}

export interface ModeStateSnapshot {
  session_id: string;
  runtime_instance_id: string;
  active_mode: RealtimeMode;
  generation: number;
  phase: ModePhase;
  last_operation_id: string | null;
  updated_at: string;
}

export interface RealtimeConnectionSnapshot {
  session_id: string;
  connection_id: string;
  state: RealtimeConnectionState;
  version: number;
  updated_at: string;
}

export interface SwitchModeCommand {
  session_id: string;
  runtime_instance_id: string;
  operation_id: string;
  trace_id: string;
  expected_generation: number;
  target_mode: RealtimeMode;
}

export interface SwitchModeResult {
  operation_id: string;
  status: ModeSwitchStatus;
  state: ModeStateSnapshot;
}

export type VoiceSessionStatus = "created" | "active" | "ended" | "failed";

export interface VoiceSessionAudioConfig {
  codec: "opus";
  sample_rate_hz: 48000;
  channels: 1;
  echo_cancellation: boolean;
  noise_suppression: boolean;
  auto_gain_control: boolean;
}

export interface VoiceSessionCapabilities {
  webrtc: boolean;
  data_channel: boolean;
  microphone: boolean;
  speaker: boolean;
  speaker_diarization: boolean;
}

export interface VoiceSession {
  id: string;
  account_id: string;
  status: VoiceSessionStatus;
  audio_config: VoiceSessionAudioConfig;
  capabilities: VoiceSessionCapabilities;
  started_at: string | null;
  ended_at: string | null;
  created_at: string;
}
