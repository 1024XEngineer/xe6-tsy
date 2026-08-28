import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StrictMode } from "react";
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
      account: { id: "acc-delivery", kind: "registered", created_at: "2026-08-01T00:00:00Z" },
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

    render(
      <StrictMode>
        <DeliverySettings />
      </StrictMode>,
    );

    expect(
      await screen.findByText(
        "单向输出的反向译文会发送到已启用的目标；启用 Webhook 后，仅投递到该 Webhook。",
      ),
    ).toBeInTheDocument();
    expect(await screen.findByText("person@example.com")).toBeInTheDocument();
    expect(screen.getByText("邮箱 · person@example.com")).toBeInTheDocument();
    expect(screen.getByText("已发送")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("checkbox", { name: "person@example.com" }));
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/account/message-preferences/email/person%40example.com",
        expect.objectContaining({ method: "PUT" }),
      );
    });
  });

  it("keeps other enabled targets when one target changes", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/message-targets")) {
        return jsonResponse({
          items: [
            {
              destination_ref: "first@example.com",
              channel: "email",
              verified: true,
              revoked_at: null,
              updated_at: "2026-08-07T00:00:00Z",
            },
            {
              destination_ref: "second@example.com",
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
              destination_ref: "first@example.com",
              enabled: true,
              verified: true,
              updated_at: "2026-08-07T00:00:00Z",
            },
            {
              account_id: "acc-delivery",
              channel: "email",
              destination_ref: "second@example.com",
              enabled: false,
              verified: true,
              updated_at: "2026-08-07T00:00:00Z",
            },
          ],
        });
      }
      if (url.endsWith("/outbound-messages")) return jsonResponse({ items: [] });
      if (
        url.endsWith("/message-preferences/email/second%40example.com") &&
        init?.method === "PUT"
      ) {
        return jsonResponse({
          account_id: "acc-delivery",
          channel: "email",
          destination_ref: "second@example.com",
          enabled: true,
          verified: true,
          updated_at: "2026-08-07T00:00:01Z",
        });
      }
      if (
        url.endsWith("/message-preferences/email/first%40example.com") &&
        init?.method === "PUT"
      ) {
        return jsonResponse({
          account_id: "acc-delivery",
          channel: "email",
          destination_ref: "first@example.com",
          enabled: false,
          verified: true,
          updated_at: "2026-08-07T00:00:02Z",
        });
      }
      return new Response("not found", { status: 404 });
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<DeliverySettings />);

    const firstTarget = await screen.findByRole("checkbox", {
      name: "first@example.com",
    });
    const secondTarget = screen.getByRole("checkbox", {
      name: "second@example.com",
    });

    expect(firstTarget).toBeChecked();
    expect(secondTarget).not.toBeChecked();

    fireEvent.click(secondTarget);
    await waitFor(() => expect(secondTarget).toBeChecked());

    fireEvent.click(firstTarget);
    await waitFor(() => {
      expect(firstTarget).not.toBeChecked();
      expect(secondTarget).toBeChecked();
    });
  });

  it("binds a webhook URL and refreshes the target list", async () => {
    let webhookBound = false;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/message-targets")) {
        return jsonResponse({
          items: webhookBound
            ? [{
                destination_ref: "primary-webhook",
                channel: "webhook",
                verified: true,
                revoked_at: null,
                updated_at: "2026-08-07T00:00:00Z",
              }]
            : [],
        });
      }
      if (url.endsWith("/message-preferences")) return jsonResponse({ items: [] });
      if (url.endsWith("/outbound-messages")) return jsonResponse({ items: [] });
      if (url.endsWith("/message-targets/webhook/bind") && init?.method === "POST") {
        webhookBound = true;
        return jsonResponse({
          destination_ref: "primary-webhook",
          channel: "webhook",
          verified: true,
          revoked_at: null,
          updated_at: "2026-08-07T00:00:00Z",
        });
      }
      return new Response("not found", { status: 404 });
    });
    vi.stubGlobal("fetch", fetchMock);

    render(<DeliverySettings />);

    const urlInput = await screen.findByRole("textbox", { name: "Webhook URL" });
    fireEvent.change(urlInput, { target: { value: "https://example.com/webhook" } });
    fireEvent.click(screen.getByRole("button", { name: "绑定 Webhook" }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/account/message-targets/webhook/bind",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify({ url: "https://example.com/webhook" }),
        }),
      );
    });
    expect(await screen.findByText("primary-webhook")).toBeInTheDocument();
    expect(urlInput).toHaveValue("");
  });
});
