import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { VoiceExperience } from "./voice-experience";

const closeWebRTC = vi.fn();

vi.mock("../lib/webrtc-session", () => ({
  openWebRTCSession: vi.fn(async () => ({
    connectionId: "conn-1",
    peerConnection: {} as RTCPeerConnection,
    localStream: {
      getTracks: () => [],
      getAudioTracks: () => [],
    } as unknown as MediaStream,
    remoteAudio: document.createElement("audio"),
    dataChannel: null,
    close: closeWebRTC,
  })),
}));

vi.mock("../lib/wake-word/wake-listener", () => {
  class WakeWordListener {
    start = vi.fn(async () => {
      this.handlers.onStatus?.("listening");
    });
    stop = vi.fn();
    getStatus = vi.fn(() => "listening" as const);
    getMediaStream = vi.fn(() => null);
    cloneAudioTracksForPeer = vi.fn(() => []);
    constructor(
      private readonly handlers: {
        onCommand: (command: "start" | "stop", keyword: string) => void;
        onStatus?: (status: string, detail?: string) => void;
      },
    ) {}
  }
  return { WakeWordListener };
});

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("VoiceExperience", () => {
  let failFirstStart = false;
  let startRequests = 0;
  let createdSessions = 0;
  let anonymousRequests = 0;
  let languageConfigVersion = 0;
  let conflictNextLanguageConfig = false;
  let languageConfigExpectedVersions: Array<number | undefined> = [];

  beforeEach(() => {
    closeWebRTC.mockClear();
    failFirstStart = false;
    startRequests = 0;
    createdSessions = 0;
    anonymousRequests = 0;
    languageConfigVersion = 0;
    conflictNextLanguageConfig = false;
    languageConfigExpectedVersions = [];
    localStorage.clear();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? "GET";

        if (url.includes("/api/v1/auth/anonymous") && method === "POST") {
          anonymousRequests += 1;
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
                expires_at: "2099-07-31T01:00:00Z",
              },
            },
            201,
          );
        }

        if (url.endsWith("/api/v1/voice-sessions") && method === "POST") {
          createdSessions += 1;
          return jsonResponse(
            {
              id: `vs-${createdSessions}`,
              account_id: "acc-1",
              status: "created",
              created_at: "2026-07-31T00:00:00Z",
            },
            201,
          );
        }

        if (url.includes("/language-configs") && method === "POST") {
          const body = JSON.parse(String(init?.body ?? "{}")) as {
            expected_version?: number;
          };
          languageConfigExpectedVersions.push(body.expected_version);
          if (conflictNextLanguageConfig) {
            conflictNextLanguageConfig = false;
            languageConfigVersion = 2;
            return jsonResponse(
              { error: { code: "version_conflict", message: "stale version" } },
              409,
            );
          }
          languageConfigVersion = Math.max(languageConfigVersion + 1, 1);
          return jsonResponse(
            {
              id: "lc-1",
              session_id: "vs-1",
              version: languageConfigVersion,
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

        if (url.endsWith("/language-config") && method === "GET") {
          return jsonResponse({
            id: "lc-1",
            session_id: "vs-1",
            version: languageConfigVersion,
            language_pairs: [
              { source: "zh-CN", target: "en-US" },
              { source: "en-US", target: "zh-CN" },
            ],
            output_routes: [],
            output_mode: "bidirectional",
            status: "active",
            effective_from: "2026-07-31T00:00:00Z",
            effective_until: null,
            created_by: "acc-1",
            created_at: "2026-07-31T00:00:00Z",
          });
        }

        if (url.includes("/realtime-ticket") && method === "POST") {
          return jsonResponse({
            ticket: "v1.demo.ticket",
            session_id: "vs-1",
            expires_at: "2026-07-31T00:01:00Z",
          });
        }

        if (url.includes("/start") && method === "POST") {
          startRequests += 1;
          if (failFirstStart && startRequests <= 2) {
            return jsonResponse(
              { error: { code: "realtime_start_failed", message: "temporary" } },
              503,
            );
          }
          return jsonResponse({
            id: `vs-${createdSessions}`,
            account_id: "acc-1",
            status: "active",
            created_at: "2026-07-31T00:00:00Z",
            started_at: "2026-07-31T00:00:01Z",
          });
        }

        if (url.includes("/api/v1/voice-sessions?") && method === "GET") {
          return jsonResponse({
            sessions: [
              {
                id: "vs-history-1",
                account_id: "acc-1",
                status: "ended",
                created_at: "2026-07-30T00:00:00Z",
                started_at: "2026-07-30T00:00:01Z",
                ended_at: "2026-07-30T00:02:01Z",
              },
            ],
            next_cursor: null,
          });
        }

        if (url.endsWith("/api/v1/account/automatic-delivery-readiness") && method === "GET") {
          return jsonResponse({ ready: true });
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
    expect(screen.getByRole("button", { name: "开始翻译" })).toBeVisible();
    expect(screen.getByRole("button", { name: "设置" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /翻译/ })).toHaveLength(1);
    expect(
      screen.getByText("轻触或说「小灵，开始翻译」"),
    ).toBeInTheDocument();
    const idleVideo = screen.getByTestId("idle-voice-video");
    expect(idleVideo).toHaveAttribute("src", "/media/loop.mp4");
    expect(idleVideo).toHaveAttribute("autoplay");
    expect(idleVideo).toHaveAttribute("loop");
    expect(idleVideo).toHaveAttribute("playsinline");
    expect(idleVideo).not.toHaveAttribute("controls");
    expect(idleVideo).toHaveAttribute("disablepictureinpicture");
    expect(idleVideo).toHaveAttribute(
      "controlslist",
      "nodownload nofullscreen noremoteplayback",
    );
    expect(screen.queryByTestId("active-voice-strands")).toBeNull();
  });

  it("opens the curved settings wheel from the header", () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));

    expect(screen.getByRole("dialog", { name: "设置" })).toBeInTheDocument();
    expect(
      screen.getByRole("listbox", { name: "设置选项" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "语言配置" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "联调会话" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "历史会话" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "用量管理" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "关于" })).toBeInTheDocument();
    expect(screen.getByText("01")).toBeInTheDocument();
    expect(screen.getByText("06")).toBeInTheDocument();
  });

  it("uses a localized custom drawer to choose the source language", () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const sourcePicker = screen.getByRole("button", { name: /源语言，当前/ });
    fireEvent.click(sourcePicker);

    expect(screen.getByRole("listbox", { name: "源语言选项" })).toBeInTheDocument();
    const russianOption = screen.getByRole("option", {
      name: /Русский.*俄语.*ru-RU/,
    });
    fireEvent.click(russianOption);

    expect(screen.queryByRole("listbox", { name: "源语言选项" })).toBeNull();
    expect(sourcePicker).toHaveAccessibleName(/源语言，当前Русский/);
  });

  it("selects single output mode and persists the preference", async () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const singleMode = screen.getByRole("button", { name: "单向输出" });
    await waitFor(() => expect(singleMode).not.toBeDisabled());
    fireEvent.click(singleMode);

    expect(singleMode).toHaveAttribute("aria-pressed", "true");
    expect(JSON.parse(localStorage.getItem("lingow-voice-config-v2") ?? "{}")).toMatchObject({
      outputMode: "single",
    });
    expect(
      screen.getByText(/反向译文自动投递，并保留 Final Turn/),
    ).toBeInTheDocument();
  });

  it("keeps the settings wheel open while showing the history preview", async () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const wheel = screen.getByRole("listbox", { name: "设置选项" });
    fireEvent.keyDown(wheel, { key: "ArrowDown" });

    expect(wheel).toBeInTheDocument();
    expect(await screen.findByText("最近 5 次会话")).toBeInTheDocument();
    expect(screen.queryByText("选择一次会话，查看完整双语记录。")).toBeNull();
  });

  it("restores the history wheel item after leaving a history session", async () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    fireEvent.keyDown(screen.getByRole("listbox", { name: "设置选项" }), {
      key: "ArrowDown",
    });
    fireEvent.click(await screen.findByRole("button", { name: /2 分钟.*已结束/ }));
    fireEvent.click(await screen.findByRole("button", { name: "返回设置" }));

    const historyOption = screen.getByRole("option", { name: "历史会话" });
    expect(historyOption).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("heading", { name: "历史会话" })).toBeInTheDocument();
  });

  it("connects through xe6-tsy APIs and shows the newest bilingual turn", async () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));

    await waitFor(() => {
      expect(screen.getByText("正在聆听")).toBeInTheDocument();
    });
    expect(screen.getByTestId("active-voice-strands")).toBeInTheDocument();
    expect(screen.queryByTestId("idle-voice-video")).toBeNull();

    await waitFor(() => {
      expect(
        screen.getByText("Hello, how can I get to the main venue?"),
      ).toBeInTheDocument();
    });
  });

  it("refreshes the language config version after a concurrent update", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const singleMode = screen.getByRole("button", { name: "单向输出" });
    await waitFor(() => expect(singleMode).not.toBeDisabled());

    conflictNextLanguageConfig = true;
    fireEvent.click(singleMode);
    await waitFor(() => expect(screen.getByText(/当前会话应用失败/)).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "双向播报" }));
    await waitFor(() => expect(screen.getByText(/已应用到当前会话/)).toBeInTheDocument());

    expect(languageConfigExpectedVersions).toEqual([undefined, 1, 2]);
  });

  it("opens the complete history from the newest subtitle", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));

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
    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));

    await waitFor(() => {
      expect(screen.getByText("正在聆听")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "停止翻译" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "开始翻译" })).toBeVisible();
    });
    expect(
      screen.getByText("轻触或说「小灵，开始翻译」"),
    ).toBeInTheDocument();
    expect(closeWebRTC).toHaveBeenCalled();
  });

  it("reuses the same anonymous account for later sessions", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "停止翻译" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "开始翻译" })).toBeVisible(),
    );
    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));
    await waitFor(() => expect(createdSessions).toBe(2));

    expect(anonymousRequests).toBe(1);
  });

  it("returns to a fresh start after a failed session startup", async () => {
    failFirstStart = true;

    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "开始翻译" })).toBeVisible();
    });

    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());
    expect(createdSessions).toBe(2);
    expect(startRequests).toBe(3);
  });
});
