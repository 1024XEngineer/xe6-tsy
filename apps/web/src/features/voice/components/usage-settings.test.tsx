import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { saveAuthSession } from "../lib/auth-session";
import { UsageSettings } from "./usage-settings";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("UsageSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    saveAuthSession({
      account: {
        id: "acc-usage",
        kind: "anonymous",
        created_at: "2026-08-01T00:00:00Z",
      },
      tokens: {
        access_token: "access-usage",
        refresh_token: "refresh-usage",
        expires_at: "2099-08-01T00:00:00Z",
      },
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).includes("/usage/summary")) {
          return jsonResponse({
            account_id: "acc-usage",
            period_start: "2026-08-01T00:00:00Z",
            period_end: "2026-09-01T00:00:00Z",
            totals: [
              {
                service_type: "asr",
                input_tokens: 0,
                output_tokens: 0,
                audio_duration_ms: 120_000,
                cost_amount: "",
                currency: "",
              },
              {
                service_type: "tts",
                input_tokens: 0,
                output_tokens: 0,
                audio_duration_ms: 120_000,
                cost_amount: "",
                currency: "",
              },
            ],
          });
        }
        return new Response("not found", { status: 404 });
      }),
    );
  });

  it("shows remaining free minutes from ASR usage", async () => {
    render(<UsageSettings />);

    expect(await screen.findByText("498")).toBeInTheDocument();
    expect(screen.getByText("分钟剩余")).toBeInTheDocument();
    expect(screen.getByText("已使用 2 分钟")).toBeInTheDocument();
    expect(screen.getByText("免费额度 500 分钟")).toBeInTheDocument();
  });
});
