/** Wake-word command labels matching public/kws/keywords.txt display names. */
export const WAKE_START_KEYWORD = "小灵，开始翻译";
export const WAKE_STOP_KEYWORD = "小灵，停止翻译";

export type WakeCommand = "start" | "stop";

export function classifyWakeKeyword(keyword: string): WakeCommand | null {
  const text = keyword.trim();
  if (!text) return null;
  if (text.includes("停止")) return "stop";
  if (text.includes("开始")) return "start";
  return null;
}
