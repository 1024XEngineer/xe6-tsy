import { NextResponse } from "next/server";

import { issueRealtimeTicket } from "@/features/voice/lib/realtime-ticket";

export const runtime = "nodejs";

type Body = {
  session_id?: string;
  account_id?: string;
};

function devTicketEnabled(): boolean {
  // Never expose HMAC minting in production builds. Local next dev still
  // requires an explicit opt-in so a publicly reachable :3000 cannot mint
  // arbitrary session tickets with REALTIME_TICKET_SECRET.
  return (
    process.env.NODE_ENV === "development" &&
    process.env.ENABLE_DEV_REALTIME_TICKET === "true"
  );
}

/**
 * Local联调 helper: mint a realtime HMAC ticket using REALTIME_TICKET_SECRET.
 * Prefer the authenticated product API:
 * `POST /api/v1/voice-sessions/{id}/realtime-ticket`.
 */
export async function POST(request: Request) {
  if (!devTicketEnabled()) {
    return NextResponse.json(
      {
        error: {
          code: "not_found",
          message:
            "Dev ticket minting is disabled. Use POST /api/v1/voice-sessions/{id}/realtime-ticket, or set ENABLE_DEV_REALTIME_TICKET=true in local next dev only.",
        },
      },
      { status: 404 },
    );
  }

  const secret = process.env.REALTIME_TICKET_SECRET?.trim() ?? "";
  if (!secret) {
    return NextResponse.json(
      {
        error: {
          code: "ticket_secret_missing",
          message:
            "REALTIME_TICKET_SECRET is not set in the Next.js environment.",
        },
      },
      { status: 500 },
    );
  }

  let body: Body;
  try {
    body = (await request.json()) as Body;
  } catch {
    return NextResponse.json(
      {
        error: {
          code: "invalid_request",
          message: "JSON body with session_id and account_id is required.",
        },
      },
      { status: 400 },
    );
  }

  const sessionId = body.session_id?.trim() ?? "";
  const accountId = body.account_id?.trim() ?? "";
  if (!sessionId || !accountId) {
    return NextResponse.json(
      {
        error: {
          code: "invalid_request",
          message: "session_id and account_id are required.",
        },
      },
      { status: 400 },
    );
  }

  try {
    const ticket = issueRealtimeTicket(secret, sessionId, accountId);
    return NextResponse.json({ ticket, session_id: sessionId });
  } catch (error) {
    const message =
      error instanceof Error ? error.message : "failed to issue ticket";
    return NextResponse.json(
      { error: { code: "ticket_issue_failed", message } },
      { status: 500 },
    );
  }
}
