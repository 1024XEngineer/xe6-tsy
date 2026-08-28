export type VoiceInteractionPolicy = "continuous" | "wake_word";
export type VoiceBusinessMode = "assistant" | "interpretation";

const STORAGE_KEY = "lingow.voice.interaction-policy";

/** Each business mode has one intentional capture behavior. */
export function effectiveVoiceInteractionPolicy(
  mode: VoiceBusinessMode,
  preferred: VoiceInteractionPolicy,
): VoiceInteractionPolicy {
  void preferred;
  return mode === "assistant" ? "wake_word" : "continuous";
}

/** Only the assistant's wake-word turn may gate capture while TTS is active. */
export function shouldSuppressMicrophoneDuringTTS(
  mode: VoiceBusinessMode,
): boolean {
  return mode !== "interpretation";
}

export function loadVoiceInteractionPolicy(
  storage: Pick<Storage, "getItem"> | undefined = typeof window === "undefined"
    ? undefined
    : window.localStorage,
): VoiceInteractionPolicy {
  try {
    return storage?.getItem(STORAGE_KEY) === "wake_word"
      ? "wake_word"
      : "continuous";
  } catch {
    return "continuous";
  }
}

export function saveVoiceInteractionPolicy(
  policy: VoiceInteractionPolicy,
  storage: Pick<Storage, "setItem"> | undefined = typeof window === "undefined"
    ? undefined
    : window.localStorage,
): void {
  try {
    storage?.setItem(STORAGE_KEY, policy);
  } catch {
    // The selected policy remains active for this page when storage is blocked.
  }
}
