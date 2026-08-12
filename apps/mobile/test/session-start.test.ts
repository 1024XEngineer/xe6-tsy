import assert from "node:assert/strict";
import test from "node:test";

import { SessionStartClient } from "../src/session-start.ts";
import { InvalidRealtimeResponseError, RealtimeApiError } from "../src/transport.ts";

const result = {
  id: "s1",
  account_id: "account-1",
  status: "active",
  audio_config: {
    codec: "opus",
    sample_rate_hz: 48000,
    channels: 1,
    echo_cancellation: true,
    noise_suppression: true,
    auto_gain_control: true,
  },
  capabilities: {
    webrtc: true,
    data_channel: true,
    microphone: true,
    speaker: true,
    speaker_diarization: true,
  },
  started_at: "2026-08-12T00:00:01Z",
  ended_at: null,
  created_at: "2026-08-12T00:00:00Z",
};

test("starts a session with an explicit initial mode and idempotency key", async () => {
  let requestInput = "";
  let requestInit: RequestInit = {};
  const client = new SessionStartClient({
    baseUrl: "https://api.example.test/",
    accessToken: async () => "access-1",
    createId: () => "start-1",
    fetchImpl: async (input, init) => {
      requestInput = input;
      requestInit = init ?? {};
      return new Response(JSON.stringify(result), { status: 200 });
    },
  });

  assert.deepEqual(await client.start("s1", "assistant"), result);
  assert.equal(requestInput, "https://api.example.test/api/v1/voice-sessions/s1/start");
  assert.equal(new Headers(requestInit.headers).get("Authorization"), "Bearer access-1");
  assert.equal(new Headers(requestInit.headers).get("Idempotency-Key"), "start-1");
  assert.deepEqual(JSON.parse(String(requestInit.body)), { initial_mode: "assistant" });
});

test("defaults assistant-capable callers to assistant", async () => {
  let body = "";
  const client = new SessionStartClient({
    baseUrl: "https://api.example.test",
    accessToken: "access-1",
    fetchImpl: async (_input, init) => {
      body = String(init?.body);
      return new Response(JSON.stringify(result), { status: 200 });
    },
  });

  await client.start("s1", undefined, "start-default");
  assert.deepEqual(JSON.parse(body), { initial_mode: "assistant" });
});

test("surfaces API errors and rejects a mismatched response", async () => {
  const rejected = new SessionStartClient({
    baseUrl: "https://api.example.test",
    accessToken: "access-1",
    fetchImpl: async () =>
      new Response(JSON.stringify({ error: { code: "runtime_operation_conflict" } }), {
        status: 409,
      }),
  });
  await assert.rejects(rejected.start("s1"), (error: unknown) =>
    error instanceof RealtimeApiError && error.code === "runtime_operation_conflict",
  );

  const mismatched = new SessionStartClient({
    baseUrl: "https://api.example.test",
    accessToken: "access-1",
    fetchImpl: async () =>
      new Response(JSON.stringify({ ...result, id: "another-session" }), { status: 200 }),
  });
  await assert.rejects(mismatched.start("s1"), InvalidRealtimeResponseError);

  const incomplete = new SessionStartClient({
    baseUrl: "https://api.example.test",
    accessToken: "access-1",
    fetchImpl: async () => {
      const { audio_config: _audioConfig, ...body } = result;
      return new Response(JSON.stringify(body), { status: 200 });
    },
  });
  await assert.rejects(incomplete.start("s1"), InvalidRealtimeResponseError);
});
