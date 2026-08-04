import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  getOrCreateAuthSession,
  loadAuthSession,
  saveAuthSession,
} from "./auth-session";

const storedAuth = {
  account: {
    id: "acc-1",
    kind: "anonymous" as const,
    created_at: "2026-08-01T00:00:00Z",
  },
  tokens: {
    access_token: "access-1",
    refresh_token: "refresh-1",
    expires_at: "2099-08-01T01:00:00Z",
  },
};

describe("auth-session", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("stores and restores the current anonymous account", () => {
    saveAuthSession(storedAuth);

    expect(loadAuthSession()).toEqual(storedAuth);
  });

  it("reuses a stored account while its access token is valid", async () => {
    saveAuthSession(storedAuth);
    const create = vi.fn();
    const refresh = vi.fn();

    await expect(getOrCreateAuthSession({ create, refresh })).resolves.toEqual(
      storedAuth,
    );
    expect(create).not.toHaveBeenCalled();
    expect(refresh).not.toHaveBeenCalled();
  });

  it("refreshes an expired account without changing its owner", async () => {
    const expired = {
      ...storedAuth,
      tokens: { ...storedAuth.tokens, expires_at: "2020-01-01T00:00:00Z" },
    };
    saveAuthSession(expired);
    const refresh = vi.fn(async () => ({
      access_token: "access-2",
      refresh_token: "refresh-2",
      expires_at: "2099-08-01T02:00:00Z",
    }));

    const result = await getOrCreateAuthSession({
      create: vi.fn(),
      refresh,
    });

    expect(refresh).toHaveBeenCalledWith("refresh-1");
    expect(result.account.id).toBe("acc-1");
    expect(result.tokens.access_token).toBe("access-2");
    expect(loadAuthSession()).toEqual(result);
  });

  it("shares one in-flight refresh across concurrent callers", async () => {
    const expired = {
      ...storedAuth,
      tokens: { ...storedAuth.tokens, expires_at: "2020-01-01T00:00:00Z" },
    };
    saveAuthSession(expired);
    let resolveRefresh: ((value: typeof storedAuth.tokens) => void) | undefined;
    const refresh = vi.fn(
      () =>
        new Promise<typeof storedAuth.tokens>((resolve) => {
          resolveRefresh = resolve;
        }),
    );
    const create = vi.fn();

    const first = getOrCreateAuthSession({ create, refresh });
    const second = getOrCreateAuthSession({ create, refresh });

    expect(refresh).toHaveBeenCalledTimes(1);
    resolveRefresh?.({
      access_token: "access-2",
      refresh_token: "refresh-2",
      expires_at: "2099-08-01T02:00:00Z",
    });
    await expect(Promise.all([first, second])).resolves.toHaveLength(2);
    expect(create).not.toHaveBeenCalled();
  });

  it("creates a replacement account when refresh fails", async () => {
    saveAuthSession({
      ...storedAuth,
      tokens: { ...storedAuth.tokens, expires_at: "2020-01-01T00:00:00Z" },
    });
    const replacement = {
      ...storedAuth,
      account: { ...storedAuth.account, id: "acc-2" },
    };
    const create = vi.fn(async () => replacement);

    await expect(
      getOrCreateAuthSession({
        create,
        refresh: vi.fn(async () => {
          throw new Error("expired refresh token");
        }),
      }),
    ).resolves.toEqual(replacement);
    expect(loadAuthSession()).toEqual(replacement);
  });
});
