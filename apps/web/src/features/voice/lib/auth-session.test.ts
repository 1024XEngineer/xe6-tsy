import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  AuthenticationRequiredError,
  clearAuthSession,
  getAuthSession,
  loadAuthSession,
  saveAuthSession,
} from "./auth-session";

const storedAuth = {
  account: {
    id: "acc-1",
    kind: "registered" as const,
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

  it("stores and restores a registered account", () => {
    saveAuthSession(storedAuth);

    expect(loadAuthSession()).toEqual(storedAuth);
    clearAuthSession();
    expect(loadAuthSession()).toBeNull();
  });

  it("rejects an old anonymous account without creating a replacement", async () => {
    localStorage.setItem(
      "lingow-auth-session-v1",
      JSON.stringify({
        ...storedAuth,
        account: { ...storedAuth.account, kind: "anonymous" },
      }),
    );
    const refresh = vi.fn();

    expect(loadAuthSession()).toBeNull();
    await expect(getAuthSession({ refresh })).rejects.toBeInstanceOf(
      AuthenticationRequiredError,
    );
    expect(refresh).not.toHaveBeenCalled();
  });

  it("reuses a stored account while its access token is valid", async () => {
    saveAuthSession(storedAuth);
    const refresh = vi.fn();

    await expect(getAuthSession({ refresh })).resolves.toEqual(storedAuth);
    expect(refresh).not.toHaveBeenCalled();
  });

  it("refreshes an expired account without changing its owner", async () => {
    saveAuthSession({
      ...storedAuth,
      tokens: { ...storedAuth.tokens, expires_at: "2020-01-01T00:00:00Z" },
    });
    const refresh = vi.fn(async () => ({
      access_token: "access-2",
      refresh_token: "refresh-2",
      expires_at: "2099-08-01T02:00:00Z",
    }));

    const result = await getAuthSession({ refresh });

    expect(refresh).toHaveBeenCalledWith("refresh-1");
    expect(result.account.id).toBe("acc-1");
    expect(result.tokens.access_token).toBe("access-2");
    expect(loadAuthSession()).toEqual(result);
  });

  it("shares one in-flight refresh across concurrent callers", async () => {
    saveAuthSession({
      ...storedAuth,
      tokens: { ...storedAuth.tokens, expires_at: "2020-01-01T00:00:00Z" },
    });
    let resolveRefresh: ((value: typeof storedAuth.tokens) => void) | undefined;
    const refresh = vi.fn(
      () =>
        new Promise<typeof storedAuth.tokens>((resolve) => {
          resolveRefresh = resolve;
        }),
    );

    const first = getAuthSession({ refresh });
    const second = getAuthSession({ refresh });

    expect(refresh).toHaveBeenCalledTimes(1);
    resolveRefresh?.({
      access_token: "access-2",
      refresh_token: "refresh-2",
      expires_at: "2099-08-01T02:00:00Z",
    });
    await expect(Promise.all([first, second])).resolves.toHaveLength(2);
  });

  it("clears expired credentials when refresh fails", async () => {
    saveAuthSession({
      ...storedAuth,
      tokens: { ...storedAuth.tokens, expires_at: "2020-01-01T00:00:00Z" },
    });

    await expect(
      getAuthSession({
        refresh: vi.fn(async () => {
          throw new Error("expired refresh token");
        }),
      }),
    ).rejects.toBeInstanceOf(AuthenticationRequiredError);
    expect(loadAuthSession()).toBeNull();
  });
});
