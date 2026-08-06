import { describe, expect, it } from "vitest";

import {
  issueRealtimeTicket,
  validateRealtimeTicket,
} from "./realtime-ticket";

const SECRET = "abcdefghijklmnopqrstuvwxyz012345";

describe("realtime-ticket", () => {
  it("issues and validates a session-scoped ticket", () => {
    const now = Date.parse("2026-07-31T00:00:00.000Z");
    const ticket = issueRealtimeTicket(
      SECRET,
      "session-1",
      "account-1",
      60_000,
      now,
    );

    const claims = validateRealtimeTicket(
      SECRET,
      ticket,
      "session-1",
      now + 1_000,
    );

    expect(claims.session_id).toBe("session-1");
    expect(claims.account_id).toBe("account-1");
  });

  it("rejects a ticket for another session", () => {
    const ticket = issueRealtimeTicket(SECRET, "session-1", "account-1");
    expect(() =>
      validateRealtimeTicket(SECRET, ticket, "session-2"),
    ).toThrow(/session mismatch/);
  });
});
