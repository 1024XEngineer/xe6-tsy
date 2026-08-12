import { describe, expect, it, vi } from "vitest";

import { ApiError } from "./http";
import { RealtimeTicketCache, withRealtimeTicket } from "./realtime-ticket-cache";

describe("RealtimeTicketCache", () => {
  it("renews a ticket before it expires and shares concurrent refreshes", async () => {
    let resolveMint: ((ticket: { ticket: string; session_id: string; expires_at: string }) => void) | undefined;
    const mint = vi.fn(
      () =>
        new Promise<{ ticket: string; session_id: string; expires_at: string }>((resolve) => {
          resolveMint = resolve;
        }),
    );
    const cache = new RealtimeTicketCache({ mint, now: () => 10_000 });
    cache.seed({ ticket: "old", session_id: "s1", expires_at: "1970-01-01T00:00:14Z" });

    const first = cache.get();
    const second = cache.get();
    expect(mint).toHaveBeenCalledTimes(1);
    resolveMint?.({ ticket: "fresh", session_id: "s1", expires_at: "2099-01-01T00:00:00Z" });

    await expect(Promise.all([first, second])).resolves.toEqual(["fresh", "fresh"]);
  });

  it("retries one unauthorized request with a newly minted ticket", async () => {
    const mint = vi.fn(async () => ({
      ticket: "fresh",
      session_id: "s1",
      expires_at: "2099-01-01T00:00:00Z",
    }));
    const cache = new RealtimeTicketCache({ mint });
    cache.seed({ ticket: "stale", session_id: "s1", expires_at: "2099-01-01T00:00:00Z" });
    const request = vi
      .fn<(ticket: string) => Promise<string>>()
      .mockRejectedValueOnce(new ApiError("unauthorized", 401, "unauthorized"))
      .mockResolvedValueOnce("ok");

    await expect(withRealtimeTicket(cache, request)).resolves.toBe("ok");
    expect(request.mock.calls).toEqual([["stale"], ["fresh"]]);
    expect(mint).toHaveBeenCalledTimes(1);
  });

  it("does not retry non-authentication failures", async () => {
    const mint = vi.fn(async () => ({
      ticket: "unused",
      session_id: "s1",
      expires_at: "2099-01-01T00:00:00Z",
    }));
    const cache = new RealtimeTicketCache({ mint });
    cache.seed({ ticket: "current", session_id: "s1", expires_at: "2099-01-01T00:00:00Z" });
    const request = vi.fn(async () => {
      throw new ApiError("conflict", 409, "mode_generation_conflict");
    });

    await expect(withRealtimeTicket(cache, request)).rejects.toMatchObject({ status: 409 });
    expect(request).toHaveBeenCalledTimes(1);
    expect(mint).not.toHaveBeenCalled();
  });
});
