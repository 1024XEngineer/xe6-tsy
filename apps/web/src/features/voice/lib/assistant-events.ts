export type AssistantReplyEvent = {
  type: "assistant.reply";
  replyId: string;
  turnId: string;
  text: string;
  language: string;
};

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object") return null;
  return value as Record<string, unknown>;
}

function readString(record: Record<string, unknown>, ...keys: string[]): string {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

/** Normalize assistant.reply messages from the realtime DataChannel. */
export function parseAssistantReply(
  payload: unknown,
): AssistantReplyEvent | null {
  const root = asRecord(payload);
  if (!root) return null;
  const nested = asRecord(root.payload);
  const eventName =
    readString(root, "type", "event") ||
    (nested ? readString(nested, "type", "event") : "");
  if (eventName !== "assistant.reply") return null;

  const source = nested ?? root;
  const text = readString(source, "text", "reply_text", "replyText");
  if (!text) return null;

  return {
    type: "assistant.reply",
    replyId:
      readString(root, "id", "event_id", "eventId") ||
      readString(source, "id", "event_id", "eventId") ||
      `assistant-dc-${Date.now()}`,
    turnId:
      readString(root, "turn_id", "turnId") ||
      readString(source, "turn_id", "turnId"),
    text,
    language: readString(source, "language", "language_code", "languageCode"),
  };
}
