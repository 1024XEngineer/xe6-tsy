export type TranslationFinalEvent = {
  type: "translation.final";
  turnId: string;
  sourceText: string;
  translatedText: string;
  sourceLanguage: string;
  targetLanguage: string;
};

export type ASRPartialEvent = {
  type: "asr.partial";
  eventVersion: 1;
  sessionId: string;
  turnId: string;
  text: string;
  stash?: string;
  sourceLanguage: string;
  occurredAt: string;
};

export type PhraseSubtitleEvent = {
  type: "phrase.subtitle";
  eventVersion: 1;
  sessionId: string;
  utteranceId: string;
  phraseSequence: number;
  sourceText: string;
  translatedText: string;
  status: "source_stable" | "translated" | "translation_failed";
  occurredAt: string;
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
    if (typeof value === "string" && value.trim()) return value;
  }
  return "";
}

/** Normalize DataChannel JSON from realtime-audio (flat or payload-wrapped). */
export function parseTranslationFinal(
  payload: unknown,
): TranslationFinalEvent | null {
  const root = asRecord(payload);
  if (!root) return null;

  const nested = asRecord(root.payload);
  const eventName =
    readString(root, "type", "event") ||
    (nested ? readString(nested, "type", "event") : "");
  if (eventName !== "translation.final") return null;

  const source = nested ?? root;
  const sourceText = readString(source, "source_text", "sourceText");
  const translatedText = readString(
    source,
    "translated_text",
    "translatedText",
  );
  if (!sourceText || !translatedText) return null;

  return {
    type: "translation.final",
    turnId:
      readString(root, "turn_id", "id") ||
      readString(source, "turn_id", "id") ||
      `dc-${Date.now()}`,
    sourceText,
    translatedText,
    sourceLanguage: readString(source, "source_language", "sourceLanguage"),
    targetLanguage: readString(source, "target_language", "targetLanguage"),
  };
}

/** Parse only versioned, ephemeral ASR snapshots from the realtime DataChannel. */
export function parseASRPartial(payload: unknown): ASRPartialEvent | null {
  const root = asRecord(payload);
  if (!root) return null;

  const nested = asRecord(root.payload);
  const eventName =
    readString(root, "type", "event") ||
    (nested ? readString(nested, "type", "event") : "");
  if (eventName !== "asr.partial") return null;

  const source = nested ?? root;
  const eventVersion = source.event_version ?? root.event_version;
  if (eventVersion !== 1) return null;
  const sessionId = readString(source, "session_id", "sessionId");
  const turnId = readString(source, "turn_id", "turnId");
  const text = readString(source, "text");
  const stash = readString(source, "stash");
  const occurredAt = readString(source, "occurred_at", "occurredAt");
  if (!sessionId || !turnId || (!text && !stash) || !occurredAt || Number.isNaN(Date.parse(occurredAt))) {
    return null;
  }

  return {
    type: "asr.partial",
    eventVersion: 1,
    sessionId,
    turnId,
    text,
    stash,
    sourceLanguage: readString(source, "source_language", "sourceLanguage"),
    occurredAt,
  };
}

/** Parse an ordered, ephemeral phrase subtitle update from the realtime DataChannel. */
export function parsePhraseSubtitle(payload: unknown): PhraseSubtitleEvent | null {
  const root = asRecord(payload);
  if (!root) return null;
  const nested = asRecord(root.payload);
  const eventName =
    readString(root, "type", "event") ||
    (nested ? readString(nested, "type", "event") : "");
  if (eventName !== "phrase.subtitle") return null;

  const source = nested ?? root;
  const eventVersion = source.event_version ?? root.event_version;
  const status = readString(source, "status") as PhraseSubtitleEvent["status"];
  const phraseSequence = Number(source.phrase_sequence ?? source.phraseSequence);
  const occurredAt = readString(source, "occurred_at", "occurredAt");
  if (
    eventVersion !== 1 ||
    !["source_stable", "translated", "translation_failed"].includes(status) ||
    !Number.isInteger(phraseSequence) ||
    phraseSequence < 1 ||
    !occurredAt ||
    Number.isNaN(Date.parse(occurredAt))
  ) {
    return null;
  }
  const sessionId = readString(source, "session_id", "sessionId");
  const utteranceId = readString(source, "utterance_id", "utteranceId");
  const sourceText = readString(source, "source_text", "sourceText");
  if (!sessionId || !utteranceId || !sourceText) return null;

  return {
    type: "phrase.subtitle",
    eventVersion: 1,
    sessionId,
    utteranceId,
    phraseSequence,
    sourceText,
    translatedText: readString(source, "translated_text", "translatedText"),
    status,
    occurredAt,
  };
}
