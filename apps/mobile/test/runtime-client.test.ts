import assert from "node:assert/strict";
import test from "node:test";

import { DEFAULT_MODE } from "../src/contracts.ts";
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

function mode(generation = 1, runtimeInstanceId = "r1", active_mode: "assistant" | "interpretation" = "interpretation") {
  return { session_id: "s1", runtime_instance_id: runtimeInstanceId, active_mode, generation, phase: "active" as const, last_operation_id: null, updated_at: "2026-01-01T00:00:00.000Z" };
}

class FakeTransport implements RealtimeTransport {
  currentMode = mode();
  nextModeError: unknown = null;
  connectionSnapshot = connection();

  async getConnection() { return this.connectionSnapshot; }
  async getRuntime() { return runtime; }
  async getMode() { return this.currentMode; }
  async switchMode(command: Parameters<RealtimeTransport["switchMode"]>[0]) {
    if (this.nextModeError) {
      const error = this.nextModeError;
      this.nextModeError = null;
      throw error;
    }
    this.currentMode = mode(command.expected_generation + 1, command.runtime_instance_id, command.target_mode);
    return { operation_id: command.operation_id, status: "applied" as const, state: this.currentMode };
  }
}

test("sync exposes connection, runtime and mode while preserving interpretation default", async () => {
  const transport = new FakeTransport();
  const client = new RuntimeClient("s1", transport);
  await client.sync();
  assert.equal(client.state.connection?.state, "connected");
  assert.equal(client.state.runtime?.runtime_state, "listening");
  assert.equal(client.state.mode?.active_mode, DEFAULT_MODE);
  assert.equal(client.state.effectiveMode, DEFAULT_MODE);
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
  transport.currentMode = mode(1, "r2");
  await assert.rejects(client.switchMode("assistant"), ModeConflictError);
  assert.deepEqual(client.state.mode?.runtime_instance_id, "r2");
  assert.equal(client.state.staleOperationIds.length, 1);
});

test("mode endpoint failure fails open to legacy interpretation", async () => {
  const transport = new FakeTransport();
  transport.getMode = async () => { throw new RealtimeApiError(404, "not_found"); };
  const client = new RuntimeClient("s1", transport);
  await client.sync();
  assert.equal(client.state.mode, null);
  assert.equal(client.state.effectiveMode, DEFAULT_MODE);
  assert.equal(client.state.status, "ready");
});

test("mode refresh failure keeps the last confirmed mode", async () => {
  const transport = new FakeTransport();
  transport.currentMode = mode(1, "r1", "assistant");
  const client = new RuntimeClient("s1", transport);
  await client.sync();
  transport.getMode = async () => { throw new RealtimeApiError(503, "service_unavailable"); };
  assert.equal(await client.refreshMode(), null);
  assert.equal(client.state.effectiveMode, "assistant");
});
