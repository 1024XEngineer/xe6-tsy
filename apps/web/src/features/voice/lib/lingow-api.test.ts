import { afterEach, describe, expect, it, vi } from "vitest";

import { startVoiceSession } from "./lingow-api";

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
    const fetchMock = vi.fn(
      async (_input: RequestInfo | URL, _init?: RequestInit) =>
        responses.shift() ?? jsonResponse({}, 500),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(
      startVoiceSession("token-1", "vs-1", "start-fixed"),
    ).resolves.toMatchObject({ id: "vs-1", status: "active" });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(
      fetchMock.mock.calls.map(([, init]) =>
        new Headers(init?.headers).get("Idempotency-Key"),
      ),
    ).toEqual(["start-fixed", "start-fixed"]);
  });
});
