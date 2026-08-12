import { ApiError } from "./http";
import type { RealtimeTicket } from "./lingow-api";

const DEFAULT_REFRESH_SKEW_MS = 5_000;

type RealtimeTicketCacheOptions = {
  mint: () => Promise<RealtimeTicket>;
  now?: () => number;
  refreshSkewMs?: number;
};

/** Keeps short-lived control-plane tickets fresh without touching WebRTC media. */
export class RealtimeTicketCache {
  private current: RealtimeTicket | null = null;
  private refresh: Promise<RealtimeTicket> | null = null;
  private readonly mint: () => Promise<RealtimeTicket>;
  private readonly now: () => number;
  private readonly refreshSkewMs: number;

  constructor(options: RealtimeTicketCacheOptions) {
    this.mint = options.mint;
    this.now = options.now ?? Date.now;
    this.refreshSkewMs = options.refreshSkewMs ?? DEFAULT_REFRESH_SKEW_MS;
  }

  seed(ticket: RealtimeTicket): void {
    this.current = ticket;
  }

  clear(): void {
    this.current = null;
  }

  async get(forceRefresh = false): Promise<string> {
    if (!forceRefresh && this.isFresh(this.current)) {
      return this.current.ticket;
    }
    if (this.refresh) return (await this.refresh).ticket;

    const refresh = this.mint();
    this.refresh = refresh;
    try {
      const ticket = await refresh;
      this.current = ticket;
      return ticket.ticket;
    } finally {
      if (this.refresh === refresh) this.refresh = null;
    }
  }

  private isFresh(ticket: RealtimeTicket | null): ticket is RealtimeTicket {
    if (!ticket?.ticket) return false;
    const expiresAt = Date.parse(ticket.expires_at);
    return Number.isFinite(expiresAt) && expiresAt - this.refreshSkewMs > this.now();
  }
}

/** A 401 is rejected before a realtime command executes, so one fresh-ticket retry is safe. */
export async function withRealtimeTicket<T>(
  tickets: RealtimeTicketCache,
  request: (ticket: string) => Promise<T>,
): Promise<T> {
  try {
    return await request(await tickets.get());
  } catch (error) {
    if (!(error instanceof ApiError) || error.status !== 401) throw error;
    return request(await tickets.get(true));
  }
}
