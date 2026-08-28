export type PlaybackLifecycleEventType =
  | "playback.started"
  | "playback.finished"
  | "playback.interrupted"
  | "playback.cancelled";

export type PlaybackLifecycleEvent = {
  type: PlaybackLifecycleEventType;
  eventId: string;
  sessionId: string;
  turnId: string;
  playbackId: string;
  sequenceNo: number;
  reason: string;
  occurredAt: string;
};

export type PlaybackLifecycleState = {
  changed: boolean;
  playing: boolean;
};

const PLAYBACK_EVENT_TYPES = new Set<PlaybackLifecycleEventType>([
  "playback.started",
  "playback.finished",
  "playback.interrupted",
  "playback.cancelled",
]);
const DEFAULT_RETIRED_PLAYBACK_LIMIT = 128;

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object") return null;
  return value as Record<string, unknown>;
}

function asEventType(value: unknown): PlaybackLifecycleEventType | null {
  if (typeof value !== "string") return null;
  const normalized = value.trim() as PlaybackLifecycleEventType;
  return PLAYBACK_EVENT_TYPES.has(normalized) ? normalized : null;
}

function readString(
  records: Array<Record<string, unknown> | null>,
  ...keys: string[]
): string {
  for (const record of records) {
    if (!record) continue;
    for (const key of keys) {
      const value = record[key];
      if (typeof value === "string" && value.trim()) return value.trim();
    }
  }
  return "";
}

function readNumber(
  records: Array<Record<string, unknown> | null>,
  ...keys: string[]
): number {
  for (const record of records) {
    if (!record) continue;
    for (const key of keys) {
      if (record[key] === undefined || record[key] === null) continue;
      const value = Number(record[key]);
      if (Number.isFinite(value)) return value;
    }
  }
  return Number.NaN;
}

/** Normalize playback lifecycle JSON from realtime-audio. */
export function parsePlaybackLifecycleEvent(
  payload: unknown,
): PlaybackLifecycleEvent | null {
  const root = asRecord(payload);
  if (!root) return null;
  const nested = asRecord(root.payload);
  const type = [root.type, root.event, nested?.type, nested?.event]
    .map(asEventType)
    .find((candidate): candidate is PlaybackLifecycleEventType => candidate !== null);
  if (!type) return null;

  const records = [nested, root];
  const eventId = readString(records, "event_id", "eventId", "id");
  const sessionId = readString(records, "session_id", "sessionId");
  const playbackId = readString(records, "playback_id", "playbackId");
  const sequenceNo = readNumber(records, "sequence_no", "sequenceNo");
  const occurredAt = readString(records, "occurred_at", "occurredAt");
  if (
    !eventId ||
    !sessionId ||
    !playbackId ||
    !Number.isInteger(sequenceNo) ||
    sequenceNo < 1 ||
    !occurredAt ||
    Number.isNaN(Date.parse(occurredAt))
  ) {
    return null;
  }

  return {
    type,
    eventId,
    sessionId,
    turnId: readString(records, "turn_id", "turnId"),
    playbackId,
    sequenceNo,
    reason: readString(records, "reason"),
    occurredAt,
  };
}

/** Track active playback IDs while rejecting late starts for settled IDs. */
export class PlaybackLifecycleTracker {
  private readonly activePlaybackIds = new Set<string>();
  private readonly retiredPlaybackIds = new Set<string>();
  private readonly retiredPlaybackOrder: string[] = [];

  constructor(
    private readonly retiredPlaybackLimit = DEFAULT_RETIRED_PLAYBACK_LIMIT,
  ) {}

  get playing(): boolean {
    return this.activePlaybackIds.size > 0;
  }

  apply(event: PlaybackLifecycleEvent): PlaybackLifecycleState {
    let changed = false;
    if (event.type === "playback.started") {
      if (!this.retiredPlaybackIds.has(event.playbackId)) {
        const previousSize = this.activePlaybackIds.size;
        this.activePlaybackIds.add(event.playbackId);
        changed = this.activePlaybackIds.size !== previousSize;
      }
      return { changed, playing: this.playing };
    }

    changed = this.activePlaybackIds.delete(event.playbackId);
    this.retire(event.playbackId);
    return { changed, playing: this.playing };
  }

  reset(): void {
    this.activePlaybackIds.clear();
    this.retiredPlaybackIds.clear();
    this.retiredPlaybackOrder.length = 0;
  }

  private retire(playbackId: string): void {
    if (this.retiredPlaybackIds.has(playbackId)) return;
    this.retiredPlaybackIds.add(playbackId);
    this.retiredPlaybackOrder.push(playbackId);
    while (this.retiredPlaybackOrder.length > this.retiredPlaybackLimit) {
      const oldest = this.retiredPlaybackOrder.shift();
      if (oldest) this.retiredPlaybackIds.delete(oldest);
    }
  }
}
