import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { VoiceExperience } from "./voice-experience";

const closeWebRTC = vi.fn();

vi.mock("../lib/webrtc-session", () => ({
  openWebRTCSession: vi.fn(async () => ({
    connectionId: "conn-1",
    peerConnection: {} as RTCPeerConnection,
    localStream: { getTracks: () => [] } as unknown as MediaStream,
    remoteAudio: document.createElement("audio"),
    dataChannel: null,
    close: closeWebRTC,
  })),
}));

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("VoiceExperience", () => {
  beforeEach(() => {
    closeWebRTC.mockClear();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? "GET";

        if (url.includes("/api/v1/auth/anonymous") && method === "POST") {
          return jsonResponse(
            {
              account: {
                id: "acc-1",
                kind: "anonymous",
                created_at: "2026-07-31T00:00:00Z",
              },
              tokens: {
                access_token: "access-1",
                refresh_token: "refresh-1",
                expires_at: "2026-07-31T01:00:00Z",
              },
            },
            201,
          );
        }

        if (url.endsWith("/api/v1/voice-sessions") && method === "POST") {
          return jsonResponse(
            {
              id: "vs-1",
              account_id: "acc-1",
              status: "created",
              created_at: "2026-07-31T00:00:00Z",
            },
            201,
          );
        }

        if (url.includes("/language-configs") && method === "POST") {
          return jsonResponse(
            {
              id: "lc-1",
              session_id: "vs-1",
              version: 1,
              language_pairs: [
                { source: "zh-CN", target: "en-US" },
                { source: "en-US", target: "zh-CN" },
              ],
              status: "active",
              effective_from: "2026-07-31T00:00:00Z",
              created_by: "acc-1",
              created_at: "2026-07-31T00:00:00Z",
            },
            201,
          );
        }

        if (url.includes("/realtime-ticket") && method === "POST") {
          return jsonResponse({
            ticket: "v1.demo.ticket",
            session_id: "vs-1",
            expires_at: "2026-07-31T00:01:00Z",
          });
        }

        if (url.includes("/start") && method === "POST") {
          return jsonResponse({
            id: "vs-1",
            account_id: "acc-1",
            status: "active",
            created_at: "2026-07-31T00:00:00Z",
            started_at: "2026-07-31T00:00:01Z",
          });
        }

        if (url.includes("/state")) {
          return jsonResponse({
            session_id: "vs-1",
            status: "active",
            runtime_state: "listening",
            current_turn_id: "turn-1",
            current_playback_id: null,
            last_error_code: null,
            retryable: false,
            runtime_updated_at: "2026-07-31T00:00:02Z",
          });
        }

        if (url.includes("/turns")) {
          return jsonResponse({
            items: [
              {
                id: "turn-1",
                session_id: "vs-1",
                source_language: "zh-CN",
                target_language: "en-US",
                source_text: "你好，请问这里怎么去主会场？",
                translated_text: "Hello, how can I get to the main venue?",
                sequence_no: 1,
                created_at: "2026-07-31T00:00:03Z",
              },
            ],
            next_cursor: null,
          });
        }

        if (url.includes("/end") && method === "POST") {
          return jsonResponse({
            id: "vs-1",
            account_id: "acc-1",
            status: "ended",
            created_at: "2026-07-31T00:00:00Z",
            ended_at: "2026-07-31T00:00:10Z",
          });
        }

        return new Response("not found", { status: 404 });
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("starts with one primary voice entry point and a settings icon", () => {
    render(<VoiceExperience />);

    expect(screen.getByText("lingow")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "开始语音会话" })).toBeVisible();
    expect(screen.getByRole("button", { name: "设置" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /语音会话/ })).toHaveLength(1);
    expect(screen.getByText("轻触开始")).toBeInTheDocument();
    expect(screen.getByTestId("idle-voice-ring")).toBeInTheDocument();
    expect(screen.queryByTestId("active-voice-strands")).toBeNull();
  });

  it("opens the curved settings wheel from the header", () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));

    expect(screen.getByRole("dialog", { name: "设置" })).toBeInTheDocument();
    expect(
      screen.getByRole("listbox", { name: "设置选项" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "语言对" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "联调会话" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "关于" })).toBeInTheDocument();
    expect(screen.getByText("01")).toBeInTheDocument();
    expect(screen.getByText("03")).toBeInTheDocument();
  });

  it("connects through xe6-tsy APIs and shows the newest bilingual turn", async () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "开始语音会话" }));

    await waitFor(() => {
      expect(screen.getByText("正在聆听")).toBeInTheDocument();
    });
    expect(screen.getByTestId("active-voice-strands")).toBeInTheDocument();

    await waitFor(() => {
      expect(
        screen.getByText("Hello, how can I get to the main venue?"),
      ).toBeInTheDocument();
    });
  });

  it("opens the complete history from the newest subtitle", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始语音会话" }));

    await waitFor(() => {
      expect(
        screen.getByText("Hello, how can I get to the main venue?"),
      ).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: /完整会话记录/ }));
    expect(screen.getByRole("dialog", { name: /会话记录/ })).toBeInTheDocument();
    expect(screen.getAllByTestId("history-turn")).toHaveLength(1);
  });

  it("ends the session from the same central control", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始语音会话" }));

    await waitFor(() => {
      expect(screen.getByText("正在聆听")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "结束语音会话" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "开始语音会话" })).toBeVisible();
    });
    expect(screen.getByText("轻触开始")).toBeInTheDocument();
    expect(closeWebRTC).toHaveBeenCalled();
  });
});
