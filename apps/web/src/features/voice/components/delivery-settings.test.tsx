import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { saveAuthSession } from "../lib/auth-session";
import { DeliverySettings } from "./delivery-settings";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("DeliverySettings", () => {
  beforeEach(() => {
    localStorage.clear();
    saveAuthSession({
      account: { id: "acc-delivery", kind: "anonymous", created_at: "2026-08-01T00:00:00Z" },
      tokens: {
        access_token: "access-delivery",
        refresh_token: "refresh-delivery",
        expires_at: "2099-08-01T00:00:00Z",
      },
    });
  });

  it("shows verified targets and recent delivery state", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/message-targets")) {
        return jsonResponse({
          items: [
            {
              destination_ref: "person@example.com",
              channel: "email",
              verified: true,
              revoked_at: null,
              updated_at: "2026-08-07T00:00:00Z",
            },
          ],
        });
      }
      if (url.endsWith("/message-preferences")) {
        return jsonResponse({
          items: [
            {
              account_id: "acc-delivery",
              channel: "email",
              destination_ref: "person@example.com",
              enabled: false,
              verified: true,
              updated_at: "2026-08-07T00:00:00Z",
            },
          ],
        });
      }
      if (url.endsWith("/outbound-messages")) {
        return jsonResponse({
          items: [
            {
              id: "message-1",
              account_id: "acc-delivery",
              channel: "email",
              destination_ref: "person@example.com",
              snapshot_version: 1,
              turns: [],
              status: "sent",
              attempts: 1,
              last_error_code: null,
              created_at: "2026-08-07T00:00:00Z",
              updated_at: "2026-08-07T00:00:01Z",
            },
          ],
        });
      }
      if (url.includes("message-preferences/email") && init?.method === "PUT") {
        return jsonResponse({
          account_id: "acc-delivery",
          channel: "email",
          destination_ref: "person@example.com",
          enabled: true,
          verified: true,
          updated_at: "2026-08-07T00:00:00Z",
        });
      }
      return new Response("not found", { status: 404 });
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<DeliverySettings />);

    expect(await screen.findByText("person@example.com")).toBeInTheDocument();
    expect(screen.getByText("邮箱 · person@example.com")).toBeInTheDocument();
    expect(screen.getByText("已发送")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "开启邮箱自动发送" }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/account/message-preferences/email",
        expect.objectContaining({ method: "PUT" }),
      );
    });
  });
});
