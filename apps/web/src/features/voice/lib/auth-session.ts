import {
  createAnonymousAccount,
  refreshAccountTokens,
  type AuthResult,
  type AuthTokens,
} from "./lingow-api";

const AUTH_STORAGE_KEY = "lingow-auth-session-v1";
const EXPIRY_SKEW_MS = 30_000;

type AuthDependencies = {
  create?: () => Promise<AuthResult>;
  refresh?: (refreshToken: string) => Promise<AuthTokens>;
};

export function loadAuthSession(): AuthResult | null {
  if (typeof window === "undefined") return null;

  try {
    const value = JSON.parse(localStorage.getItem(AUTH_STORAGE_KEY) ?? "null") as
      | AuthResult
      | null;
    if (
      !value?.account?.id ||
      !value.tokens?.access_token ||
      !value.tokens?.refresh_token ||
      !value.tokens?.expires_at
    ) {
      return null;
    }
    return value;
  } catch {
    return null;
  }
}

export function saveAuthSession(auth: AuthResult): void {
  if (typeof window === "undefined") return;
  localStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify(auth));
}

export async function getOrCreateAuthSession(
  dependencies: AuthDependencies = {},
): Promise<AuthResult> {
  const create = dependencies.create ?? createAnonymousAccount;
  const refresh = dependencies.refresh ?? refreshAccountTokens;
  const stored = loadAuthSession();
  const expiresAt = stored ? Date.parse(stored.tokens.expires_at) : Number.NaN;

  if (stored && expiresAt > Date.now() + EXPIRY_SKEW_MS) {
    return stored;
  }

  if (stored) {
    try {
      const tokens = await refresh(stored.tokens.refresh_token);
      const refreshed = { account: stored.account, tokens };
      saveAuthSession(refreshed);
      return refreshed;
    } catch {
      // A rejected refresh means this browser needs a new anonymous owner.
    }
  }

  const created = await create();
  saveAuthSession(created);
  return created;
}
