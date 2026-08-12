export type AssistantReplyEvent = {
  type: "assistant.reply";
  eventId: string;
  turnId: string;
  text: string;
  language: string;
};

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object") return null;
  return value as Record<string, unknown>;
}

function readString(
  record: Record<string, unknown>,
  ...keys: string[]
): string {
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

/** Normalize flat or payload-wrapped assistant replies from the realtime DataChannel. */
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
  const text = readString(source, "text");
  if (!text) return null;

  return {
    type: "assistant.reply",
    eventId:
      readString(root, "event_id", "id") ||
      readString(source, "event_id", "id"),
    turnId: readString(root, "turn_id") || readString(source, "turn_id"),
    text,
    language: readString(source, "language"),
  };
}
