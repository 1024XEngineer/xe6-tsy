import { afterEach, describe, expect, it, vi } from "vitest";

import { listVoiceSessions, startVoiceSession } from "./lingow-api";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("startVoiceSession", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("retries a transient start failure with the same idempotency key", async () => {
    const responses = [
      jsonResponse(
        { error: { code: "realtime_start_failed", message: "temporary" } },
        503,
      ),
      jsonResponse({ id: "vs-1", status: "active" }),
    ];
    const fetchMock = vi.fn(async () => responses.shift() ?? jsonResponse({}, 500));
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      startVoiceSession("token-1", "vs-1", "start-fixed"),
    ).resolves.toMatchObject({ id: "vs-1", status: "active" });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(
      (fetchMock.mock.calls as unknown as Array<[
        RequestInfo | URL,
        RequestInit | undefined,
      ]>).map(([, init]) => new Headers(init?.headers).get("Idempotency-Key")),
    ).toEqual(["start-fixed", "start-fixed"]);
  });

  it("cancels the retry delay when the caller aborts", async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn(async () =>
      jsonResponse(
        { error: { code: "realtime_start_failed", message: "temporary" } },
        503,
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const pending = startVoiceSession(
      "token-1",
      "vs-1",
      "start-fixed",
      controller.signal,
    );
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    controller.abort();

    await expect(pending).rejects.toMatchObject({ name: "AbortError" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("listVoiceSessions", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("loads the current account history newest first", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({
        sessions: [
          {
            id: "vs-history-1",
            account_id: "acc-1",
            status: "ended",
            created_at: "2026-08-04T08:00:00Z",
            started_at: "2026-08-04T08:00:01Z",
            ended_at: "2026-08-04T08:12:01Z",
          },
        ],
        next_cursor: null,
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const result = await listVoiceSessions("access-1", { limit: 12 });

    expect(result.sessions[0]?.id).toBe("vs-history-1");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/voice-sessions?limit=12",
      expect.objectContaining({ cache: "no-store" }),
    );
    const calls = fetchMock.mock.calls as unknown as Array<[
      RequestInfo | URL,
      RequestInit,
    ]>;
    const init = calls[0]?.[1];
    expect(new Headers(init.headers).get("Authorization")).toBe(
      "Bearer access-1",
    );
  });
});
