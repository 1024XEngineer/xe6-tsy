import { createHmac, timingSafeEqual } from "node:crypto";

const TICKET_VERSION = "v1";
const MIN_SECRET_BYTES = 32;
const DEFAULT_TTL_MS = 60_000;

export type TicketClaims = {
  session_id: string;
  account_id: string;
  expires_at: string;
};

function requireSecret(secret: string): Buffer {
  const bytes = Buffer.from(secret, "utf8");
  if (bytes.length < MIN_SECRET_BYTES) {
    throw new Error(
      `REALTIME_TICKET_SECRET must be at least ${MIN_SECRET_BYTES} bytes`,
    );
  }
  return bytes;
}

function sign(secret: Buffer, value: string): string {
  return createHmac("sha256", secret).update(value).digest("base64url");
}

/** Issue an HMAC realtime ticket matching xe6-tsy realtime/v1 ticket.go. */
export function issueRealtimeTicket(
  secret: string,
  sessionId: string,
  accountId: string,
  ttlMs = DEFAULT_TTL_MS,
  now = Date.now(),
): string {
  if (!sessionId.trim() || !accountId.trim()) {
    throw new Error("session_id and account_id are required");
  }

  const key = requireSecret(secret);
  const claims: TicketClaims = {
    session_id: sessionId,
    account_id: accountId,
    expires_at: new Date(now + ttlMs).toISOString(),
  };
  const payload = Buffer.from(JSON.stringify(claims), "utf8").toString(
    "base64url",
  );
  const signed = `${TICKET_VERSION}.${payload}`;
  return `${signed}.${sign(key, signed)}`;
}

export function validateRealtimeTicket(
  secret: string,
  token: string,
  sessionId: string,
  now = Date.now(),
): TicketClaims {
  const key = requireSecret(secret);
  const first = token.indexOf(".");
  const last = token.lastIndexOf(".");
  if (first <= 0 || last <= first + 1 || last === token.length - 1) {
    throw new Error("invalid realtime ticket");
  }

  const version = token.slice(0, first);
  const payload = token.slice(first + 1, last);
  const signature = token.slice(last + 1);
  if (version !== TICKET_VERSION) {
    throw new Error("invalid realtime ticket");
  }

  const signed = `${version}.${payload}`;
  const expected = sign(key, signed);
  const left = Buffer.from(signature);
  const right = Buffer.from(expected);
  if (left.length !== right.length || !timingSafeEqual(left, right)) {
    throw new Error("invalid realtime ticket");
  }

  const claims = JSON.parse(
    Buffer.from(payload, "base64url").toString("utf8"),
  ) as TicketClaims;

  if (claims.session_id !== sessionId) {
    throw new Error("realtime ticket session mismatch");
  }
  if (!claims.account_id || !claims.expires_at) {
    throw new Error("invalid realtime ticket");
  }
  if (!(new Date(claims.expires_at).getTime() > now)) {
    throw new Error("realtime ticket expired");
  }
  return claims;
}
