import {
  type ConnectionSnapshot,
  type ModeStateSnapshot,
  type RuntimeSnapshot,
  type SwitchModeCommand,
  type SwitchModeResult,
} from "./contracts.ts";

export type FetchLike = (
  input: string,
  init?: RequestInit,
) => Promise<Response>;

export interface RealtimeTransport {
  getConnection(sessionId: string): Promise<ConnectionSnapshot>;
  getRuntime(sessionId: string): Promise<RuntimeSnapshot>;
  getMode(sessionId: string): Promise<ModeStateSnapshot>;
  switchMode(command: SwitchModeCommand): Promise<SwitchModeResult>;
}

export class RealtimeApiError extends Error {
  readonly status: number;
  readonly code: string | null;

  constructor(status: number, code: string | null, message = "realtime request failed") {
    super(message);
    this.name = "RealtimeApiError";
    this.status = status;
    this.code = code;
  }
}

export class InvalidRealtimeResponseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "InvalidRealtimeResponseError";
  }
}

export interface HttpRealtimeTransportOptions {
  baseUrl: string;
  ticket: string | (() => string | Promise<string>);
  fetchImpl?: FetchLike;
}

export class HttpRealtimeTransport implements RealtimeTransport {
  private readonly baseUrl: string;
  private readonly ticket: HttpRealtimeTransportOptions["ticket"];
  private readonly fetchImpl: FetchLike;

  constructor(options: HttpRealtimeTransportOptions) {
    this.baseUrl = options.baseUrl.replace(/\/$/, "");
    this.ticket = options.ticket;
    this.fetchImpl = options.fetchImpl ?? fetch;
    if (!this.baseUrl || !this.ticket) {
      throw new Error("baseUrl and ticket are required");
    }
  }

  async getConnection(sessionId: string): Promise<ConnectionSnapshot> {
    return this.get(`/realtime/v1/sessions/${encodeURIComponent(sessionId)}/connection`);
  }

  async getRuntime(sessionId: string): Promise<RuntimeSnapshot> {
    return this.get(`/realtime/v1/sessions/${encodeURIComponent(sessionId)}/runtime`);
  }

  async getMode(sessionId: string): Promise<ModeStateSnapshot> {
    return this.get(`/realtime/v1/sessions/${encodeURIComponent(sessionId)}/mode`);
  }

  async switchMode(command: SwitchModeCommand): Promise<SwitchModeResult> {
    return this.request<SwitchModeResult>(
      `/realtime/v1/sessions/${encodeURIComponent(command.session_id)}/mode`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": `mode:${command.operation_id}`,
        },
        body: JSON.stringify(command),
      },
    );
  }

  private get<T>(path: string): Promise<T> {
    return this.request<T>(path, { method: "GET", cache: "no-store" });
  }

  private async request<T>(path: string, init: RequestInit): Promise<T> {
    const ticket = typeof this.ticket === "function" ? await this.ticket() : this.ticket;
    const response = await this.fetchImpl(`${this.baseUrl}${path}`, {
      ...init,
      headers: {
        Authorization: `Bearer ${ticket}`,
        ...(init.headers ?? {}),
      },
    });
    const text = await response.text();
    let body: unknown = null;
    if (text) {
      try {
        body = JSON.parse(text);
      } catch {
        throw new InvalidRealtimeResponseError("realtime response is not JSON");
      }
    }
    if (!response.ok) {
      const code =
        typeof body === "object" && body !== null && "error" in body &&
        typeof body.error === "object" && body.error !== null && "code" in body.error &&
        typeof body.error.code === "string"
          ? body.error.code
          : null;
      throw new RealtimeApiError(response.status, code);
    }
    if (body === null || typeof body !== "object") {
      throw new InvalidRealtimeResponseError("realtime response body is empty");
    }
    return body as T;
  }
}
