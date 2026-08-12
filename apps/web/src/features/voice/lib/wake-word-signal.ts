import { cancelAllTTSAudioPlayback } from "./tts-playback";

export const WAKE_WORD_DETECTED_TYPE = "wake_word.detected" as const;
export const WAKE_WORD_DETECTED_EVENT_VERSION = 1 as const;

export type WakeWordDetectedSignal = {
  type: typeof WAKE_WORD_DETECTED_TYPE;
  event_version: typeof WAKE_WORD_DETECTED_EVENT_VERSION;
  signal_id: string;
  detected_at: string;
};

type WakeWordSignalSession = Pick<
  import("./webrtc-session").WebRTCSessionHandles,
  "localStream" | "dataChannel"
>;

type WakeWordSignalDependencies = {
  cancelPlayback?: () => void;
  createSignalId?: () => string;
  now?: () => Date;
};

let fallbackSignalSequence = 0;

function createWakeWordSignalId(): string {
  const cryptoApi = globalThis.crypto;
  if (cryptoApi?.randomUUID) {
    return cryptoApi.randomUUID();
  }

  let entropy = "0";
  try {
    if (cryptoApi?.getRandomValues) {
      const values = cryptoApi.getRandomValues(new Uint32Array(2));
      entropy = `${values[0]!.toString(36)}${values[1]!.toString(36)}`;
    }
  } catch {
    // Some embedded browsers expose crypto but reject getRandomValues.
  }
  fallbackSignalSequence += 1;
  return `wake-${Date.now().toString(36)}-${entropy}-${fallbackSignalSequence}`;
}

export type WakeWordSignalResult =
  | { ok: true; signal: WakeWordDetectedSignal }
  | {
      ok: false;
      reason: "data_channel_not_open" | "send_failed";
      error?: unknown;
    };

/**
 * Interrupt browser playback and notify realtime that local KWS fired.
 * A transport failure is deliberately returned as data: callers must keep the
 * existing WebRTC and business session alive so the user can retry.
 */
export function sendWakeWordDetectedSignal(
  session: WakeWordSignalSession,
  dependencies: WakeWordSignalDependencies = {},
): WakeWordSignalResult {
  const cancelPlayback =
    dependencies.cancelPlayback ?? cancelAllTTSAudioPlayback;
  cancelPlayback();

  // TTS playback normally mutes uplink tracks. Wake audio must be available to
  // Command Gate immediately, without waiting for the normal resume delay.
  for (const track of session.localStream.getAudioTracks()) {
    track.enabled = true;
  }

  const channel = session.dataChannel;
  if (!channel || channel.readyState !== "open") {
    return { ok: false, reason: "data_channel_not_open" };
  }

  const signal: WakeWordDetectedSignal = {
    type: WAKE_WORD_DETECTED_TYPE,
    event_version: WAKE_WORD_DETECTED_EVENT_VERSION,
    signal_id: dependencies.createSignalId?.() ?? createWakeWordSignalId(),
    detected_at: (dependencies.now?.() ?? new Date()).toISOString(),
  };

  try {
    channel.send(JSON.stringify(signal));
    return { ok: true, signal };
  } catch (error) {
    return { ok: false, reason: "send_failed", error };
  }
}
