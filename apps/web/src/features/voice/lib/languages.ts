export type LanguageCode = "zh-CN" | "en-US";

export const LANGUAGE_LABELS: Record<LanguageCode, string> = {
  "zh-CN": "中文",
  "en-US": "English",
};

export const SUPPORTED_LANGUAGES: LanguageCode[] = ["zh-CN", "en-US"];

export function languageLabel(code: string): string {
  return LANGUAGE_LABELS[code as LanguageCode] ?? code;
}

export function isLanguageCode(value: string): value is LanguageCode {
  return value === "zh-CN" || value === "en-US";
}

export type VoiceSessionConfig = {
  sourceLanguage: LanguageCode;
  targetLanguage: LanguageCode;
};

export const DEFAULT_VOICE_CONFIG: VoiceSessionConfig = {
  sourceLanguage: "zh-CN",
  targetLanguage: "en-US",
};

export function bilingualPairs(config: VoiceSessionConfig): Array<{
  source: LanguageCode;
  target: LanguageCode;
}> {
  return [
    { source: config.sourceLanguage, target: config.targetLanguage },
    { source: config.targetLanguage, target: config.sourceLanguage },
  ];
}

export function formatActivePair(config: VoiceSessionConfig): string {
  return `${languageLabel(config.sourceLanguage)} ⇄ ${languageLabel(config.targetLanguage)}`;
}
