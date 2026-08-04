export type LanguageCode = string;

export const LANGUAGE_LABELS: Record<string, string> = {
  "zh-CN": "中文",
  "en-US": "English",
  "ja-JP": "日本語",
  "ko-KR": "한국어",
  "fr-FR": "Français",
  "de-DE": "Deutsch",
  "ru-RU": "Русский",
  "pt-BR": "Português",
  "th-TH": "ไทย",
  "id-ID": "Bahasa Indonesia",
  "vi-VN": "Tiếng Việt",
};

export const SUPPORTED_LANGUAGES: LanguageCode[] = [
  "zh-CN",
  "en-US",
  "ja-JP",
  "ko-KR",
  "fr-FR",
  "de-DE",
  "ru-RU",
  "pt-BR",
  "th-TH",
  "id-ID",
  "vi-VN",
];

export function languageLabel(code: string): string {
  return LANGUAGE_LABELS[code as LanguageCode] ?? code;
}

export function isLanguageCode(value: string): value is LanguageCode {
  return /^[a-z]{2,3}(?:-[A-Z][a-z]{3})?-[A-Z]{2}$/.test(value);
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
