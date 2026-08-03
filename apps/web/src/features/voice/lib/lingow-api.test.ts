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
    const fetchMock = vi
      .fn<() => Promise<Response>>()
      .mockResolvedValueOnce(
        jsonResponse(
          { error: { code: "realtime_start_failed", message: "temporary" } },
          503,
        ),
      )
      .mockResolvedValueOnce(
        jsonResponse({ id: "vs-1", status: "active" }),
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
