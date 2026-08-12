import { DEFAULT_MODE, isMode, type Mode } from "./contracts.ts";
import {
  InvalidRealtimeResponseError,
  RealtimeApiError,
  type FetchLike,
} from "./transport.ts";

export type VoiceSessionStatus = "created" | "active" | "ended" | "failed";

export interface VoiceSessionStartResult {
  id: string;
  account_id: string;
  status: VoiceSessionStatus;
  started_at: string | null;
  ended_at: string | null;
  created_at: string;
}

export interface SessionStartClientOptions {
  baseUrl: string;
  accessToken: string | (() => string | Promise<string>);
  fetchImpl?: FetchLike;
  createId?: () => string;
}

/** Calls the API control plane; media setup remains owned by the host app. */
export class SessionStartClient {
  private readonly baseUrl: string;
  private readonly accessToken: SessionStartClientOptions["accessToken"];
  private readonly fetchImpl: FetchLike;
  private readonly createId: () => string;

  constructor(options: SessionStartClientOptions) {
    this.baseUrl = options.baseUrl.replace(/\/$/, "");
    this.accessToken = options.accessToken;
    this.fetchImpl = options.fetchImpl ?? fetch;
    this.createId = options.createId ?? defaultId;
    if (!this.baseUrl || !this.accessToken) {
      throw new Error("baseUrl and accessToken are required");
    }
  }

  async start(
    sessionId: string,
    initialMode: Mode = DEFAULT_MODE,
    idempotencyKey = this.createId(),
    signal?: AbortSignal,
  ): Promise<VoiceSessionStartResult> {
    if (!sessionId.trim() || !isMode(initialMode) || !idempotencyKey.trim()) {
      throw new Error("session start request is invalid");
    }
    const accessToken =
      typeof this.accessToken === "function"
        ? await this.accessToken()
        : this.accessToken;
    if (!accessToken.trim()) throw new Error("access token is empty");

    const request: RequestInit = {
      method: "POST",
      headers: {
        Authorization: `Bearer ${accessToken}`,
        "Content-Type": "application/json",
        "Idempotency-Key": idempotencyKey,
      },
      body: JSON.stringify({ initial_mode: initialMode }),
    };
    if (signal) request.signal = signal;
    const response = await this.fetchImpl(
      `${this.baseUrl}/api/v1/voice-sessions/${encodeURIComponent(sessionId)}/start`,
      request,
    );
    const body = await parseBody(response);
    if (!response.ok) {
      throw new RealtimeApiError(response.status, errorCode(body), "session start failed");
    }
    if (!isStartResult(body, sessionId)) {
      throw new InvalidRealtimeResponseError("session start response is invalid");
    }
    return body;
  }
}

async function parseBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    throw new InvalidRealtimeResponseError("session start response is not JSON");
  }
}

function errorCode(body: unknown): string | null {
  if (typeof body !== "object" || body === null || !("error" in body)) return null;
  const error = body.error;
  if (typeof error !== "object" || error === null || !("code" in error)) return null;
  return typeof error.code === "string" ? error.code : null;
}

function isStartResult(body: unknown, sessionId: string): body is VoiceSessionStartResult {
  if (typeof body !== "object" || body === null) return false;
  const value = body as Record<string, unknown>;
  return (
    value.id === sessionId &&
    typeof value.account_id === "string" &&
    isVoiceSessionStatus(value.status) &&
    isNullableTimestamp(value.started_at) &&
    isNullableTimestamp(value.ended_at) &&
    isTimestamp(value.created_at)
  );
}

function isVoiceSessionStatus(value: unknown): value is VoiceSessionStatus {
  return value === "created" || value === "active" || value === "ended" || value === "failed";
}

function isNullableTimestamp(value: unknown): boolean {
  return value === null || isTimestamp(value);
}

function isTimestamp(value: unknown): boolean {
  return typeof value === "string" && Number.isFinite(Date.parse(value));
}

function defaultId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return `start-${crypto.randomUUID()}`;
  }
  return `start-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
