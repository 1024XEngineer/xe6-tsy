import {
  refreshAccountTokens,
  type AuthResult,
  type AuthTokens,
} from "./lingow-api";

const AUTH_STORAGE_KEY = "lingow-auth-session-v1";
const EXPIRY_SKEW_MS = 30_000;
let authSessionRequest: Promise<AuthResult> | null = null;

type AuthDependencies = {
  refresh?: (refreshToken: string) => Promise<AuthTokens>;
};

export class AuthenticationRequiredError extends Error {
  constructor() {
    super("请先登录");
    this.name = "AuthenticationRequiredError";
  }
}

export function loadAuthSession(): AuthResult | null {
  if (typeof window === "undefined") return null;

  try {
    const value = JSON.parse(localStorage.getItem(AUTH_STORAGE_KEY) ?? "null") as
      | AuthResult
      | null;
    if (
      !value?.account?.id ||
      value.account.kind !== "registered" ||
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
  if (auth.account.kind !== "registered") {
    throw new AuthenticationRequiredError();
  }
  localStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify(auth));
}

export function clearAuthSession(): void {
  if (typeof window === "undefined") return;
  localStorage.removeItem(AUTH_STORAGE_KEY);
}

export function getAuthSession(
  dependencies: AuthDependencies = {},
): Promise<AuthResult> {
  if (authSessionRequest) return authSessionRequest;

  const request = resolveAuthSession(dependencies);
  authSessionRequest = request;
  void request.then(
    () => {
      if (authSessionRequest === request) authSessionRequest = null;
    },
    () => {
      if (authSessionRequest === request) authSessionRequest = null;
    },
  );
  return request;
}

async function resolveAuthSession(
  dependencies: AuthDependencies,
): Promise<AuthResult> {
  const refresh = dependencies.refresh ?? refreshAccountTokens;
  const stored = loadAuthSession();
  if (!stored) throw new AuthenticationRequiredError();
  const expiresAt = stored ? Date.parse(stored.tokens.expires_at) : Number.NaN;

  if (stored && expiresAt > Date.now() + EXPIRY_SKEW_MS) {
    return stored;
  }

  try {
    const tokens = await refresh(stored.tokens.refresh_token);
    const refreshed = { account: stored.account, tokens };
    saveAuthSession(refreshed);
    return refreshed;
  } catch {
    // Another tab may have rotated the same refresh token successfully.
    const latest = loadAuthSession();
    if (
      latest &&
      latest.tokens.refresh_token !== stored.tokens.refresh_token &&
      Date.parse(latest.tokens.expires_at) > Date.now() + EXPIRY_SKEW_MS
    ) {
      return latest;
    }
  }

  clearAuthSession();
  throw new AuthenticationRequiredError();
}
