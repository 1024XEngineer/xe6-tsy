import assert from "node:assert/strict";
import test from "node:test";

import { RuntimeClient } from "../src/runtime-client.ts";
import type { ReconnectPolicy } from "../src/reconnect.ts";
import type { RealtimeTransport } from "../src/transport.ts";
import type { SwitchModeCommand, SwitchModeResult } from "../src/contracts.ts";

const mode = { session_id: "s1", runtime_instance_id: "r1", active_mode: "interpretation" as const, generation: 1, phase: "active" as const, last_operation_id: null, updated_at: "2026-01-01T00:00:00.000Z" };
const runtime = { session_id: "s1", start_operation_id: "start1", runtime_state: "listening" as const, current_turn_id: null, current_playback_id: null, last_error_code: null, updated_at: "2026-01-01T00:00:00.000Z" };

class ReconnectTransport implements RealtimeTransport {
  calls = 0;
  failFirstRead = false;
  async getConnection(_sessionId: string) { this.calls += 1; return { session_id: "s1", connection_id: "c1", state: this.calls > 1 ? "connected" as const : "disconnected" as const, version: this.calls, updated_at: "2026-01-01T00:00:00.000Z" }; }
  async getRuntime() { return runtime; }
  async getMode() { return mode; }
  async switchMode(_command: SwitchModeCommand): Promise<SwitchModeResult> { throw new Error("unused"); }
}

test("reconnect policy is injectable and refreshes snapshots after recovery", async () => {
  const transport = new ReconnectTransport();
  const policy: ReconnectPolicy = { next: (attempt) => ({ waitMs: attempt === 1 ? 0 : 1, continue: attempt < 3 }) };
  const client = new RuntimeClient("s1", transport, { reconnectPolicy: policy, sleep: async () => undefined });
  client.observeConnection(await transport.getConnection("s1"));
  assert.equal(client.state.status, "reconnecting");
  await client.reconnect();
  assert.equal(client.state.connection?.state, "connected");
  assert.equal(client.state.status, "ready");
  assert.equal(transport.calls, 2);
});

test("reconnect policy retries a transient connection read", async () => {
  const transport = new ReconnectTransport();
  const original = transport.getConnection.bind(transport);
  transport.getConnection = async (sessionId: string) => {
    if (transport.failFirstRead) {
      transport.failFirstRead = false;
      throw new Error("temporary network failure");
    }
    return original(sessionId);
  };
  const policy: ReconnectPolicy = { next: (attempt) => ({ waitMs: 0, continue: attempt <= 3 }) };
  const client = new RuntimeClient("s1", transport, { reconnectPolicy: policy, sleep: async () => undefined });
  client.observeConnection({ session_id: "s1", connection_id: "c1", state: "disconnected", version: 1, updated_at: "2026-01-01T00:00:00.000Z" });
  transport.failFirstRead = true;
  await client.reconnect();
  assert.equal(client.state.connection?.state, "connected");
});
