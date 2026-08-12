import assert from "node:assert/strict";
import test from "node:test";

import {
  LEGACY_MODE_FALLBACK,
  type ConnectionSnapshot,
  type SwitchModeResult,
} from "../src/contracts.ts";
import { ModeConflictError, RuntimeClient } from "../src/runtime-client.ts";
import { RealtimeApiError, type RealtimeTransport } from "../src/transport.ts";

const connection = (state: "connected" | "disconnected" = "connected") => ({
  session_id: "s1",
  connection_id: "c1",
  state,
  version: 1,
  updated_at: "2026-01-01T00:00:00.000Z",
});

const runtime = { session_id: "s1", start_operation_id: "start1", runtime_state: "listening" as const, current_turn_id: null, current_playback_id: null, last_error_code: null, updated_at: "2026-01-01T00:00:00.000Z" };

function mode(generation = 1, runtimeInstanceId = "r1", active_mode: "assistant" | "interpretation" = "interpretation"): import("../src/contracts.ts").ModeStateSnapshot {
  return { session_id: "s1", runtime_instance_id: runtimeInstanceId, active_mode, generation, phase: "active" as const, last_operation_id: null, updated_at: "2026-01-01T00:00:00.000Z" };
}

class FakeTransport implements RealtimeTransport {
  currentMode = mode();
  nextModeError: unknown = null;
  connectionSnapshot: ConnectionSnapshot = connection();

  async getConnection() { return this.connectionSnapshot; }
  async getRuntime() { return runtime; }
  async getMode() { return this.currentMode; }
  async switchMode(command: Parameters<RealtimeTransport["switchMode"]>[0]): Promise<SwitchModeResult> {
    if (this.nextModeError) {
      const error = this.nextModeError;
      this.nextModeError = null;
      throw error;
    }
    this.currentMode = {
      ...mode(command.expected_generation + 1, command.runtime_instance_id, command.target_mode),
      last_operation_id: command.operation_id,
    };
    return { operation_id: command.operation_id, status: "applied" as const, state: this.currentMode };
  }
}

test("sync exposes connection, runtime and mode while preserving interpretation default", async () => {
  const transport = new FakeTransport();
  const client = new RuntimeClient("s1", transport);
  await client.sync();
  assert.equal(client.state.connection?.state, "connected");
  assert.equal(client.state.runtime?.runtime_state, "listening");
  assert.equal(client.state.mode?.active_mode, LEGACY_MODE_FALLBACK);
  assert.equal(client.state.effectiveMode, LEGACY_MODE_FALLBACK);
});

test("generation conflict refreshes mode and marks the attempted operation stale", async () => {
  const transport = new FakeTransport();
  const client = new RuntimeClient("s1", transport, { createId: (() => { let n = 0; return () => `id${++n}`; })() });
  await client.sync();
  transport.nextModeError = new RealtimeApiError(409, "mode_generation_conflict");
  await assert.rejects(client.switchMode("assistant"), (error: unknown) => {
    if (!(error instanceof ModeConflictError)) return false;
    assert.equal(error.refreshedMode?.generation, 1);
    assert.equal(client.isOperationStale(error.staleOperationId), true);
    return true;
  });
});

test("runtime instance refresh invalidates pending operation identity", async () => {
  const transport = new FakeTransport();
  const client = new RuntimeClient("s1", transport, { createId: (() => { let n = 0; return () => `id${++n}`; })() });
  await client.sync();
  transport.nextModeError = new RealtimeApiError(409, "mode_runtime_instance_mismatch");
  transport.currentMode = {
    ...mode(1, "r2"),
    updated_at: "2026-01-01T00:00:01.000Z",
  };
  await assert.rejects(client.switchMode("assistant"), ModeConflictError);
  assert.deepEqual(client.state.mode?.runtime_instance_id, "r2");
  assert.equal(client.state.staleOperationIds.length, 1);
});

test("only an explicit legacy mode capability gap falls back to interpretation", async () => {
  const transport = new FakeTransport();
  transport.getMode = async () => { throw new RealtimeApiError(501, "not_implemented"); };
  const client = new RuntimeClient("s1", transport);
  await client.sync();
  assert.equal(client.state.mode, null);
  assert.equal(client.state.effectiveMode, LEGACY_MODE_FALLBACK);
  assert.equal(client.state.status, "ready");
});

test("mode authorization or dependency failures leave the client in error", async () => {
  for (const [status, code] of [[401, "unauthorized"], [503, "service_unavailable"]] as const) {
    const transport = new FakeTransport();
    transport.getMode = async () => { throw new RealtimeApiError(status, code); };
    const client = new RuntimeClient("s1", transport);
    await client.sync();
    assert.equal(client.state.status, "error", code);
    assert.equal(client.state.errorCode, code);
  }
});

test("mode refresh failure preserves state but rejects and reports error", async () => {
  const transport = new FakeTransport();
  transport.currentMode = mode(1, "r1", "assistant");
  const client = new RuntimeClient("s1", transport);
  await client.sync();
  transport.getMode = async () => { throw new RealtimeApiError(503, "service_unavailable"); };
  await assert.rejects(client.refreshMode(), RealtimeApiError);
  assert.equal(client.state.effectiveMode, "assistant");
  assert.equal(client.state.status, "error");
});

test("does not downgrade a previously mode-capable session to legacy fallback", async () => {
  const transport = new FakeTransport();
  const client = new RuntimeClient("s1", transport);
  await client.sync();
  transport.getMode = async () => { throw new RealtimeApiError(501, "not_implemented"); };

  await assert.rejects(client.refreshMode(), RealtimeApiError);
  assert.equal(client.state.status, "error");
  assert.equal(client.state.mode?.runtime_instance_id, "r1");
});

test("sync does not report non-ready connection states as ready", async () => {
  for (const [state, expected] of [
    ["new", "syncing"],
    ["connecting", "syncing"],
    ["closed", "error"],
  ] as const) {
    const transport = new FakeTransport();
    transport.connectionSnapshot = { ...connection(), state };
    const client = new RuntimeClient("s1", transport);
    await client.sync();
    assert.equal(client.state.status, expected, state);
  }
});

test("late snapshots cannot roll connection, runtime, or mode backward", () => {
  const client = new RuntimeClient("s1", new FakeTransport());
  client.observeConnection({ ...connection(), connection_id: "c1", version: 2, updated_at: "2026-01-01T00:00:02.000Z" });
  assert.equal(client.observeConnection({ ...connection(), connection_id: "c1", version: 1, updated_at: "2026-01-01T00:00:03.000Z" }), false);
  client.observeConnection({ ...connection(), connection_id: "c2", version: 1, updated_at: "2026-01-01T00:00:04.000Z" });
  assert.equal(client.observeConnection({ ...connection(), connection_id: "c1", version: 9, updated_at: "2026-01-01T00:00:09.000Z" }), false);

  client.observeRuntime({ ...runtime, start_operation_id: "start1", updated_at: "2026-01-01T00:00:02.000Z" });
  client.observeRuntime({ ...runtime, start_operation_id: "start2", updated_at: "2026-01-01T00:00:03.000Z" });
  assert.equal(client.observeRuntime({ ...runtime, start_operation_id: "start1", updated_at: "2026-01-01T00:00:09.000Z" }), false);

  client.observeMode(mode(1, "r1"));
  client.observeMode({ ...mode(1, "r2"), updated_at: "2025-12-31T23:59:00.000Z" });
  assert.equal(client.observeMode({ ...mode(9, "r1"), updated_at: "2026-01-01T00:00:09.000Z" }), false);
  assert.equal(client.state.mode?.runtime_instance_id, "r2");
});

test("an older mode read cannot replace a later observed runtime", async () => {
  let resolveRead!: (value: ReturnType<typeof mode>) => void;
  const transport = new FakeTransport();
  transport.getMode = async () => new Promise((resolve) => { resolveRead = resolve; });
  const client = new RuntimeClient("s1", transport);

  const pending = client.refreshMode();
  client.observeMode({ ...mode(1, "r2", "assistant"), updated_at: "2025-01-01T00:00:00.000Z" });
  resolveRead({ ...mode(9, "r1"), updated_at: "2027-01-01T00:00:00.000Z" });

  assert.equal((await pending)?.runtime_instance_id, "r2");
  assert.equal(client.state.mode?.runtime_instance_id, "r2");
});

test("an older mode read failure cannot replace a later observed runtime", async () => {
  let rejectRead!: (error: unknown) => void;
  const transport = new FakeTransport();
  transport.getMode = async () => new Promise((_resolve, reject) => { rejectRead = reject; });
  const client = new RuntimeClient("s1", transport);

  const pending = client.refreshMode();
  client.observeMode(mode(1, "r2", "assistant"));
  rejectRead(new RealtimeApiError(503, "service_unavailable"));

  assert.equal((await pending)?.runtime_instance_id, "r2");
  assert.equal(client.state.status, "idle");
  assert.equal(client.state.errorCode, null);
});

test("late successful mode response is discarded after runtime replacement", async () => {
  let resolveSwitch!: (value: Awaited<ReturnType<RealtimeTransport["switchMode"]>>) => void;
  const transport = new FakeTransport();
  transport.switchMode = async (command): Promise<SwitchModeResult> => new Promise<SwitchModeResult>((resolve) => {
    resolveSwitch = resolve;
  });
  const client = new RuntimeClient("s1", transport, { createId: () => "operation-1" });
  await client.sync();

  const pending = client.switchMode("assistant");
  client.observeMode({ ...mode(1, "r2"), updated_at: "2026-01-01T00:00:02.000Z" });
  const lastOperationId = "operation-1";
  resolveSwitch({
    operation_id: lastOperationId,
    status: "applied",
    state: { ...mode(2, "r1", "assistant"), last_operation_id: lastOperationId, updated_at: "2026-01-01T00:00:03.000Z" },
  });

  await assert.rejects(pending, (error: unknown) =>
    error instanceof ModeConflictError && error.code === "stale_mode_response",
  );
  assert.equal(client.state.mode?.runtime_instance_id, "r2");
  assert.equal(client.isOperationStale(lastOperationId), true);
  assert.equal(client.state.status, "ready");
  assert.equal(client.state.errorCode, null);
});

test("rejects malformed snapshots before they poison client state", () => {
  const client = new RuntimeClient("s1", new FakeTransport());

  assert.throws(() =>
    client.observeConnection({ ...connection(), connection_id: "", version: 0 }),
  );
  assert.throws(() =>
    client.observeRuntime({ ...runtime, start_operation_id: "", updated_at: "invalid" }),
  );
  assert.throws(() =>
    client.observeMode({ ...mode(), phase: "invalid" as "active" }),
  );
  assert.equal(client.state.connection, null);
  assert.equal(client.state.runtime, null);
  assert.equal(client.state.mode, null);
});

test("rejects a mode response for another operation", async () => {
  const transport = new FakeTransport();
  transport.switchMode = async (command) => ({
    operation_id: "another-operation",
    status: "applied",
    state: {
      ...mode(command.expected_generation + 1, command.runtime_instance_id, command.target_mode),
      last_operation_id: "another-operation",
    },
  });
  const client = new RuntimeClient("s1", transport, { createId: () => "operation-1" });
  await client.sync();

  await assert.rejects(client.switchMode("assistant"), /mode response does not match operation/);
  assert.equal(client.state.mode?.active_mode, "interpretation");
  assert.equal(client.state.status, "error");
  assert.deepEqual(client.state.lastModeCommand, {
    operationId: "operation-1",
    targetMode: "assistant",
    status: "failed",
    errorCode: "mode response does not match operation",
  });
});

test("projects applied, unchanged, and conflict command results", async () => {
  const transport = new FakeTransport();
  const ids = ["trace-1", "operation-1", "trace-2", "operation-2", "trace-3", "operation-3"];
  const client = new RuntimeClient("s1", transport, { createId: () => ids.shift() ?? "id" });
  await client.sync();

  await client.switchMode("assistant");
  assert.deepEqual(client.state.lastModeCommand, {
    operationId: "operation-1",
    targetMode: "assistant",
    status: "applied",
    errorCode: null,
  });

  transport.switchMode = async (command) => ({
    operation_id: command.operation_id,
    status: "unchanged",
    state: { ...transport.currentMode, last_operation_id: command.operation_id },
  });
  await client.switchMode("assistant");
  assert.equal(client.state.lastModeCommand?.status, "unchanged");

  transport.nextModeError = new RealtimeApiError(409, "mode_generation_conflict");
  transport.switchMode = FakeTransport.prototype.switchMode.bind(transport);
  await assert.rejects(client.switchMode("interpretation"), ModeConflictError);
  assert.deepEqual(client.state.lastModeCommand, {
    operationId: "operation-3",
    targetMode: "interpretation",
    status: "conflict",
    errorCode: "mode_generation_conflict",
  });
});

test("conflict refresh failure remains an error", async () => {
  const transport = new FakeTransport();
  const client = new RuntimeClient("s1", transport, { createId: () => "operation-1" });
  await client.sync();
  transport.nextModeError = new RealtimeApiError(409, "mode_generation_conflict");
  transport.getMode = async () => { throw new RealtimeApiError(503, "service_unavailable"); };

  await assert.rejects(client.switchMode("assistant"), (error: unknown) =>
    error instanceof RealtimeApiError && error.code === "service_unavailable",
  );
  assert.equal(client.state.status, "error");
  assert.equal(client.state.errorCode, "service_unavailable");
  assert.equal(client.state.lastModeCommand?.status, "conflict");
});
