import { afterEach, describe, expect, it, vi } from "vitest";

import {
  bindEmailTarget,
  bindWeChatTarget,
  createLanguageConfig,
  getAccountUsageSummary,
  hasReadyAutomaticTarget,
  listSessionTurns,
  listMessagePreferences,
  listMessageTargets,
  listOutboundMessages,
  listSupportedLanguages,
  listVoiceSessions,
  putMessagePreference,
  requestEmailBindVerification,
  revokeMessageTarget,
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

describe("createLanguageConfig", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("sends output routes and the expected version for an active switch", async () => {
    const fetchMock = vi.fn(async () =>
      jsonResponse({
        id: "cfg-2",
        session_id: "vs-1",
        version: 2,
        language_pairs: [],
        output_routes: [],
        output_mode: "single",
        status: "active",
        effective_from: "2026-08-07T00:00:00Z",
        effective_until: null,
        created_by: "acc-1",
        created_at: "2026-08-07T00:00:00Z",
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await createLanguageConfig("access-1", "vs-1", {
      sourceLanguage: "zh-CN",
      targetLanguage: "en-US",
      outputMode: "single",
    }, 1);

    const [, init] = fetchMock.mock.calls[0] as unknown as [RequestInfo, RequestInit];
    expect(JSON.parse(String(init.body))).toMatchObject({
      expected_version: 1,
      output_routes: [
        { target_language: "en-US", tts_enabled: true, delivery_enabled: false },
        { target_language: "zh-CN", tts_enabled: false, delivery_enabled: true },
      ],
    });
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

describe("hasReadyAutomaticTarget", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("uses server-computed automatic delivery readiness", async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ ready: true }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(hasReadyAutomaticTarget("access-1")).resolves.toBe(true);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/account/automatic-delivery-readiness",
      expect.objectContaining({ cache: "no-store" }),
    );
  });

  it("returns false when the server reports delivery is unavailable", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => jsonResponse({ ready: false })),
    );

    await expect(hasReadyAutomaticTarget("access-1")).resolves.toBe(false);
  });
});

describe("delivery settings API", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("uses account delivery routes and targets the selected destination", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("verification-codes")) return new Response(null, { status: 202 });
      if (init?.method === "DELETE") return new Response(null, { status: 204 });
      if (url.endsWith("/message-targets?channel=email")) {
        return jsonResponse({ items: [] });
      }
      if (url.endsWith("/message-targets")) return jsonResponse({ items: [] });
      if (url.endsWith("/message-preferences")) return jsonResponse({ items: [] });
      if (url.endsWith("/outbound-messages")) return jsonResponse({ items: [] });
      if (url.includes("message-preferences/email")) {
        return jsonResponse({
          account_id: "acc-1",
          channel: "email",
          destination_ref: "email-1",
          enabled: true,
          verified: true,
          updated_at: "2026-08-07T00:00:00Z",
        });
      }
      if (url.includes("email/bind")) {
        return jsonResponse({
          destination_ref: "email-1",
          channel: "email",
          verified: true,
          revoked_at: null,
          updated_at: "2026-08-07T00:00:00Z",
        });
      }
      if (url.includes("wechat/bind")) {
        return jsonResponse({
          destination_ref: "wechat-1",
          channel: "wechat",
          verified: true,
          revoked_at: null,
          updated_at: "2026-08-07T00:00:00Z",
        });
      }
      return jsonResponse({}, 500);
    });
    vi.stubGlobal("fetch", fetchMock);

    await listMessageTargets("access-1", "email");
    await listMessagePreferences("access-1");
    await listOutboundMessages("access-1");
    await putMessagePreference("access-1", "email", "email-1", true);
    await requestEmailBindVerification("access-1", "person@example.com");
    await bindEmailTarget("access-1", "dev:person@example.com");
    await bindWeChatTarget("access-1", "oauth-code");
    await revokeMessageTarget("access-1", "email", "email-1");

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/account/message-targets?channel=email",
      expect.objectContaining({ cache: "no-store" }),
    );
    const preferenceCall = fetchMock.mock.calls.find(([input]) =>
      String(input).includes("message-preferences/email/email-1"),
    );
    expect(JSON.parse(String(preferenceCall?.[1]?.body))).toEqual({ enabled: true });
    expect(new Headers(preferenceCall?.[1]?.headers).get("Idempotency-Key")).toMatch(
      /^preference-/,
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
