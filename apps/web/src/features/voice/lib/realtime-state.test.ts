import { describe, expect, it } from "vitest";

import type { ModeStateSnapshot } from "./realtime-api";
import { ModeSnapshotTracker } from "./realtime-state";

function snapshot(
  runtimeInstanceId: string,
  generation: number,
  updatedAt: string,
): ModeStateSnapshot {
  return {
    session_id: "session-1",
    runtime_instance_id: runtimeInstanceId,
    active_mode: generation % 2 === 0 ? "assistant" : "interpretation",
    generation,
    phase: "active",
    last_operation_id: null,
    updated_at: updatedAt,
  };
}

describe("ModeSnapshotTracker", () => {
  it("rejects an older generation from the current runtime", () => {
    const tracker = new ModeSnapshotTracker();
    expect(tracker.observe(snapshot("runtime-1", 3, "2026-08-11T00:00:03Z"))).toBe(true);
    expect(tracker.observe(snapshot("runtime-1", 2, "2026-08-11T00:00:04Z"))).toBe(false);
    expect(tracker.current?.generation).toBe(3);
  });

  it("never restores a retired runtime from a late successful response", () => {
    const tracker = new ModeSnapshotTracker();
    tracker.observe(snapshot("runtime-1", 1, "2026-08-11T00:00:01Z"));
    tracker.observe(snapshot("runtime-2", 1, "2026-08-11T00:00:02Z"));

    expect(tracker.observe(snapshot("runtime-1", 9, "2026-08-11T00:00:09Z"))).toBe(false);
    expect(tracker.current?.runtime_instance_id).toBe("runtime-2");
  });

  it("reset permits a new session to reuse an instance identifier", () => {
    const tracker = new ModeSnapshotTracker();
    tracker.observe(snapshot("runtime-1", 1, "2026-08-11T00:00:01Z"));
    tracker.observe(snapshot("runtime-2", 1, "2026-08-11T00:00:02Z"));
    tracker.reset();

    expect(tracker.observe(snapshot("runtime-1", 1, "2026-08-11T00:00:01Z"))).toBe(true);
  });
});
