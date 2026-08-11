/**
 * Wake-trigger catalog matching public/kws/keywords.txt @display names.
 * Add a new phrase by appending WAKE_TRIGGERS and keywords.txt line(s).
 */

export type WakeCommand = "start" | "stop" | "listen";

export type WakeTriggerId =
  | "start_translate"
  | "stop_translate"
  | "attention";

export type WakeTrigger = {
  id: WakeTriggerId;
  command: WakeCommand;
  /** Canonical display name; also a keywords.txt `@…` suffix. */
  label: string;
  /** Additional KWS display names, including pronunciation and mode aliases. */
  aliases?: readonly string[];
};

export const WAKE_TRIGGERS: readonly WakeTrigger[] = [
  {
    id: "start_translate",
    command: "start",
    label: "小灵，开始翻译",
    aliases: ["小林，开始翻译", "小灵，开始对话", "小林，开始对话"],
  },
  {
    id: "stop_translate",
    command: "stop",
    label: "小灵，停止翻译",
    aliases: ["小林，停止翻译", "小灵，停止对话", "小林，停止对话"],
  },
  {
    id: "attention",
    command: "listen",
    // Double phrase so KWS does not steal start/stop mid-utterance.
    label: "小灵小灵",
    aliases: ["小林小林"],
  },
];

export const WAKE_START_KEYWORD =
  WAKE_TRIGGERS.find((t) => t.id === "start_translate")!.label;
export const WAKE_STOP_KEYWORD =
  WAKE_TRIGGERS.find((t) => t.id === "stop_translate")!.label;
export const WAKE_LISTEN_KEYWORD =
  WAKE_TRIGGERS.find((t) => t.id === "attention")!.label;

type PhraseHit = { trigger: WakeTrigger; phrase: string };

function allPhrases(): PhraseHit[] {
  const hits: PhraseHit[] = [];
  for (const trigger of WAKE_TRIGGERS) {
    hits.push({ trigger, phrase: trigger.label });
    for (const alias of trigger.aliases ?? []) {
      hits.push({ trigger, phrase: alias });
    }
  }
  return hits;
}

/** Longest phrase first so short triggers do not steal longer phrases. */
const PHRASES_BY_LENGTH = allPhrases().sort(
  (a, b) => b.phrase.length - a.phrase.length,
);

export function resolveWakeTrigger(keyword: string): WakeTrigger | null {
  const text = keyword.trim();
  if (!text) return null;

  const exact = PHRASES_BY_LENGTH.find((h) => h.phrase === text);
  if (exact) return exact.trigger;

  for (const hit of PHRASES_BY_LENGTH) {
    if (text.includes(hit.phrase)) return hit.trigger;
  }
  return null;
}

export function classifyWakeKeyword(keyword: string): WakeCommand | null {
  return resolveWakeTrigger(keyword)?.command ?? null;
}
