import {
  isLanguageCode,
  type LanguageCode,
  type InterpretationOutputMode,
  type VoiceSessionConfig,
} from "./languages";

const SESSION_STORAGE_KEY = "lingow-voice-config-v2";

export function loadVoiceConfig(
  fallback: VoiceSessionConfig,
): VoiceSessionConfig {
  if (typeof window === "undefined") return fallback;

  try {
    const raw = localStorage.getItem(SESSION_STORAGE_KEY);
    if (!raw) return fallback;
    const saved = JSON.parse(raw) as Partial<VoiceSessionConfig>;
    return normalizeVoiceConfig({
      sourceLanguage: saved.sourceLanguage ?? fallback.sourceLanguage,
      targetLanguage: saved.targetLanguage ?? fallback.targetLanguage,
      outputMode: saved.outputMode ?? fallback.outputMode,
    });
  } catch {
    return fallback;
  }
}

export function saveVoiceConfig(config: VoiceSessionConfig): void {
  if (typeof window === "undefined") return;
  localStorage.setItem(SESSION_STORAGE_KEY, JSON.stringify(config));
}

export function normalizeVoiceConfig(
  config: VoiceSessionConfig,
): VoiceSessionConfig {
  const source = isLanguageCode(config.sourceLanguage)
    ? config.sourceLanguage
    : ("zh-CN" satisfies LanguageCode);
  let target = isLanguageCode(config.targetLanguage)
    ? config.targetLanguage
    : ("en-US" satisfies LanguageCode);

  if (source === target) {
    target = source === "zh-CN" ? "en-US" : "zh-CN";
  }

  const outputMode: InterpretationOutputMode =
    config.outputMode === "single" ? "single" : "bidirectional";

  return { sourceLanguage: source, targetLanguage: target, outputMode };
}
