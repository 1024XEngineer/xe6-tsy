/**
 * Fixed wake-word catalog matching public/kws/keywords.txt @display names.
 * Business commands are interpreted by realtime only after this local gate fires.
 */

export type WakeTrigger = {
  id: "attention";
  /** Canonical display name; also a keywords.txt `@…` suffix. */
  label: string;
};

export const WAKE_TRIGGERS: readonly WakeTrigger[] = [
  {
    id: "attention",
    label: "小灵小灵",
  },
];

export const WAKE_LISTEN_KEYWORD =
  WAKE_TRIGGERS.find((t) => t.id === "attention")!.label;

export type WakePhraseMatch = { trigger: WakeTrigger; phrase: string };

export function resolveWakePhrase(keyword: string): WakePhraseMatch | null {
  const text = keyword.trim();
  if (!text) return null;
  const trigger = WAKE_TRIGGERS.find((item) => item.label === text);
  return trigger ? { trigger, phrase: trigger.label } : null;
}

export function resolveWakeTrigger(keyword: string): WakeTrigger | null {
  return resolveWakePhrase(keyword)?.trigger ?? null;
}
