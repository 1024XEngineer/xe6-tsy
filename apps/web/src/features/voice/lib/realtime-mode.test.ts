import { afterEach, describe, expect, it, vi } from "vitest";

import {
  getRealtimeModeState,
  switchRealtimeMode,
  type SwitchModeCommand,
} from "./realtime-api";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("realtime mode API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("reads the realtime-owned mode snapshot", async () => {
    const fetchMock = vi.fn<
      (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
    >(async () =>
      jsonResponse({
        session_id: "session-1",
        runtime_instance_id: "runtime-1",
        active_mode: "interpretation",
        generation: 1,
        phase: "active",
        last_operation_id: null,
        updated_at: "2026-08-11T00:00:00Z",
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(getRealtimeModeState("ticket-1", "session-1")).resolves.toMatchObject({
      active_mode: "interpretation",
      generation: 1,
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/realtime/v1/sessions/session-1/mode",
      expect.objectContaining({
        headers: { Authorization: "Bearer ticket-1" },
        cache: "no-store",
      }),
    );
  });

  it("sends a typed compare-and-switch command with an idempotency key", async () => {
    const fetchMock = vi.fn<
      (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
    >(async () =>
      jsonResponse({
        operation_id: "operation-1",
        status: "applied",
        state: {
          session_id: "session-1",
          runtime_instance_id: "runtime-1",
          active_mode: "assistant",
          generation: 2,
          phase: "active",
          last_operation_id: "operation-1",
          updated_at: "2026-08-11T00:00:01Z",
        },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const command: SwitchModeCommand = {
      session_id: "session-1",
      runtime_instance_id: "runtime-1",
      operation_id: "operation-1",
      trace_id: "trace-1",
      expected_generation: 1,
      target_mode: "assistant",
    };

    await expect(
      switchRealtimeMode("ticket-1", "session-1", command, "mode-key-1"),
    ).resolves.toMatchObject({ status: "applied" });
    const [, init] = fetchMock.mock.calls[0]!;
    expect(init).toMatchObject({
      method: "POST",
      headers: {
        Authorization: "Bearer ticket-1",
        "Content-Type": "application/json",
        "Idempotency-Key": "mode-key-1",
      },
    });
    expect(JSON.parse(String(init?.body))).toEqual(command);
  });

  it("preserves the typed conflict code for snapshot refresh callers", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        jsonResponse(
          { error: { code: "mode_generation_conflict", message: "stale" } },
          409,
        ),
      ),
    );
    await expect(
      switchRealtimeMode("ticket-1", "session-1", {
        session_id: "session-1",
        runtime_instance_id: "runtime-1",
        operation_id: "operation-1",
        trace_id: "trace-1",
        expected_generation: 1,
        target_mode: "assistant",
      }),
    ).rejects.toMatchObject({ status: 409, code: "mode_generation_conflict" });
  });
});
