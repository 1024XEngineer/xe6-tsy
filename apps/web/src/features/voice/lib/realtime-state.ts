import type { ModeStateSnapshot } from "./realtime-api";

/**
 * Keeps the browser projection monotonic while realtime runtimes are replaced.
 * A generation is only comparable inside one runtime instance. Once a newer
 * runtime is observed, responses from the retired instance stay stale even if
 * they arrive later with a larger timestamp.
 */
export class ModeSnapshotTracker {
  private currentSnapshot: ModeStateSnapshot | null = null;
  private readonly retiredRuntimeIds = new Set<string>();

  get current(): ModeStateSnapshot | null {
    return this.currentSnapshot;
  }

  observe(next: ModeStateSnapshot): boolean {
    const previous = this.currentSnapshot;
    if (!previous) {
      this.currentSnapshot = next;
      return true;
    }

    if (previous.runtime_instance_id === next.runtime_instance_id) {
      if (
        next.generation < previous.generation ||
        (next.generation === previous.generation &&
          Date.parse(next.updated_at) <= Date.parse(previous.updated_at))
      ) {
        return false;
      }
      this.currentSnapshot = next;
      return true;
    }

    if (
      this.retiredRuntimeIds.has(next.runtime_instance_id) ||
      Date.parse(next.updated_at) <= Date.parse(previous.updated_at)
    ) {
      return false;
    }
    this.retiredRuntimeIds.add(previous.runtime_instance_id);
    this.currentSnapshot = next;
    return true;
  }

  reset(): void {
    this.currentSnapshot = null;
    this.retiredRuntimeIds.clear();
  }
}
