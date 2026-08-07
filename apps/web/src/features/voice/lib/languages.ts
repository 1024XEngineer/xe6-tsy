export type LanguageCode = string;

export const LANGUAGE_LABELS: Record<string, string> = {
  "it-IT": "Italian",
  "es-ES": "Spanish",
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

export const LANGUAGE_TRANSLATIONS: Record<string, string> = {
  "it-IT": "Italian",
  "es-ES": "Spanish",
  "zh-CN": "中文（简体）",
  "en-US": "英语（美国）",
  "ja-JP": "日语",
  "ko-KR": "韩语",
  "fr-FR": "法语",
  "de-DE": "德语",
  "ru-RU": "俄语",
  "pt-BR": "葡萄牙语（巴西）",
  "th-TH": "泰语",
  "id-ID": "印度尼西亚语",
  "vi-VN": "越南语",
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
  "it-IT",
  "es-ES",
];

export function languageLabel(code: string): string {
  return LANGUAGE_LABELS[code as LanguageCode] ?? code;
}

export function languageTranslation(code: string): string {
  return LANGUAGE_TRANSLATIONS[code] ?? code;
}

export function isLanguageCode(value: string): value is LanguageCode {
  return SUPPORTED_LANGUAGES.includes(value);
}

export type InterpretationOutputMode = "bidirectional" | "single";

export type VoiceSessionConfig = {
  sourceLanguage: LanguageCode;
  targetLanguage: LanguageCode;
  outputMode: InterpretationOutputMode;
};

export type LanguageOutputRoute = {
  target_language: LanguageCode;
  tts_enabled: boolean;
  delivery_enabled: boolean;
};

export const DEFAULT_VOICE_CONFIG: VoiceSessionConfig = {
  sourceLanguage: "zh-CN",
  targetLanguage: "en-US",
  outputMode: "bidirectional",
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

export function outputRoutes(config: VoiceSessionConfig): LanguageOutputRoute[] {
  if (config.outputMode === "single") {
    return [
      {
        target_language: config.targetLanguage,
        tts_enabled: true,
        delivery_enabled: false,
      },
      {
        target_language: config.sourceLanguage,
        tts_enabled: false,
        delivery_enabled: true,
      },
    ];
  }
  return [
    {
      target_language: config.targetLanguage,
      tts_enabled: true,
      delivery_enabled: false,
    },
    {
      target_language: config.sourceLanguage,
      tts_enabled: true,
      delivery_enabled: false,
    },
  ];
}

export function formatActivePair(config: VoiceSessionConfig): string {
  return `${languageLabel(config.sourceLanguage)} ⇄ ${languageLabel(config.targetLanguage)}`;
}
