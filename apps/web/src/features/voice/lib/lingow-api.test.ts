import { afterEach, describe, expect, it, vi } from "vitest";

import {
  getAccountUsageSummary,
  listSessionTurns,
  listSupportedLanguages,
  listVoiceSessions,
  startVoiceSession,
} from "./lingow-api";

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

describe("getAccountUsageSummary", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("loads usage totals for a requested period", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({
        account_id: "acc-1",
        period_start: "2026-08-01T00:00:00Z",
        period_end: "2026-09-01T00:00:00Z",
        totals: [
          {
            service_type: "asr",
            input_tokens: 0,
            output_tokens: 0,
            audio_duration_ms: 90_000,
            cost_amount: "",
            currency: "",
          },
        ],
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const result = await getAccountUsageSummary(
      "access-1",
      "2026-08-01T00:00:00Z",
      "2026-09-01T00:00:00Z",
    );

    expect(result.totals[0]?.audio_duration_ms).toBe(90_000);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/usage/summary?period_start=2026-08-01T00%3A00%3A00Z&period_end=2026-09-01T00%3A00%3A00Z",
      expect.objectContaining({ cache: "no-store" }),
    );
    const calls = fetchMock.mock.calls as unknown as Array<[
      RequestInfo | URL,
      RequestInit,
    ]>;
    expect(new Headers(calls[0]?.[1].headers).get("Authorization")).toBe(
      "Bearer access-1",
    );
  });
});

describe("authenticated catalog and turn pagination", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("sends the access token when loading supported languages", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({ languages: [] }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await listSupportedLanguages("access-1");

    const [, init] = fetchMock.mock.calls[0] as unknown as [
      RequestInfo,
      RequestInit,
    ];
    expect(new Headers(init.headers).get("Authorization")).toBe(
      "Bearer access-1",
    );
  });

  it("passes the cursor when loading the next turn page", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({ items: [], next_cursor: null }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await listSessionTurns("access-1", "session-1", 100, "cursor-2");

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/voice-sessions/session-1/turns?limit=100&cursor=cursor-2",
      expect.objectContaining({ cache: "no-store" }),
    );
  });
});
