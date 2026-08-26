import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { saveAuthSession } from "../lib/auth-session";
import { END_REQUEST_TIMEOUT_MS } from "../hooks/use-voice-session";
import { VoiceExperience } from "./voice-experience";

const closeWebRTC = vi.fn();
const wakeWordSend = vi.fn();
const uplinkTrack = { enabled: true };
let dataMessageHandler: ((payload: unknown) => void) | undefined;
let wakeHandler: ((keyword: string) => void) | undefined;

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

vi.mock("../lib/webrtc-session", () => ({
  openWebRTCSession: vi.fn(async (options: { onDataMessage: (payload: unknown) => void }) => {
    dataMessageHandler = options.onDataMessage;
    return {
      connectionId: "conn-1",
      peerConnection: {} as RTCPeerConnection,
      localStream: {
        getTracks: () => [],
        getAudioTracks: () => [uplinkTrack],
      } as unknown as MediaStream,
      remoteAudio: document.createElement("audio"),
      wakeWordChannel: {
        readyState: "open",
        send: wakeWordSend,
      } as unknown as RTCDataChannel,
      controlDataChannel: {
        readyState: "open",
      } as unknown as RTCDataChannel,
      close: closeWebRTC,
    };
  }),
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
        onWake: (keyword: string) => void;
        onStatus?: (status: string, detail?: string) => void;
      },
    ) {
      wakeHandler = handlers.onWake;
    }
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
  let startInitialModes: Array<string | undefined> = [];
  let createdSessions = 0;
  let endRequests = 0;
  let endRequestGate: Promise<Response> | null = null;
  let sessionCreationGate: Promise<Response> | null = null;
  let anonymousRequests = 0;
  let modeRequests = 0;
  let activeMode: "assistant" | "interpretation" = "interpretation";
  let modeGeneration = 1;
  let languageConfigVersion = 0;
  let conflictNextLanguageConfig = false;
  let automaticDeliveryReady = true;
  let automaticOutputStatuses: Array<{
    turn_id: string;
    status: "fallback_pending" | "fallback_played" | "restored";
    updated_at: string;
  }> = [];
  let languageConfigExpectedVersions: Array<number | undefined> = [];
  let languageConfigRequests: Array<{
    expected_version?: number;
    output_routes?: Array<{
      target_language: string;
      tts_enabled: boolean;
      delivery_enabled: boolean;
    }>;
  }> = [];

  beforeEach(() => {
    vi.stubEnv("NEXT_PUBLIC_LINGOW_INITIAL_MODE", "assistant");
    closeWebRTC.mockClear();
    wakeWordSend.mockClear();
    uplinkTrack.enabled = true;
    dataMessageHandler = undefined;
    wakeHandler = undefined;
    failFirstStart = false;
    startRequests = 0;
    startInitialModes = [];
    createdSessions = 0;
    endRequests = 0;
    endRequestGate = null;
    sessionCreationGate = null;
    anonymousRequests = 0;
    modeRequests = 0;
    activeMode = "interpretation";
    modeGeneration = 1;
    languageConfigVersion = 0;
    conflictNextLanguageConfig = false;
    automaticDeliveryReady = true;
    automaticOutputStatuses = [];
    languageConfigExpectedVersions = [];
    languageConfigRequests = [];
    localStorage.clear();
    saveAuthSession({
      account: {
        id: "acc-1",
        kind: "registered",
        created_at: "2026-07-31T00:00:00Z",
      },
      tokens: {
        access_token: "access-1",
        refresh_token: "refresh-1",
        expires_at: "2099-07-31T01:00:00Z",
      },
    });
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
          if (sessionCreationGate) return sessionCreationGate;
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
            output_routes?: Array<{
              target_language: string;
              tts_enabled: boolean;
              delivery_enabled: boolean;
            }>;
          };
          languageConfigExpectedVersions.push(body.expected_version);
          languageConfigRequests.push(body);
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
            output_routes: [
              { target_language: "en-US", tts_enabled: true, delivery_enabled: false },
              { target_language: "zh-CN", tts_enabled: true, delivery_enabled: false },
            ],
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
          const body = init?.body ? JSON.parse(String(init.body)) as { initial_mode?: string } : {};
          startInitialModes.push(body.initial_mode);
          if (body.initial_mode === "assistant" || body.initial_mode === "interpretation") {
            activeMode = body.initial_mode;
          }
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
          return jsonResponse({ ready: automaticDeliveryReady });
        }

        if (url.endsWith("/automatic-output-status") && method === "GET") {
          return jsonResponse({ items: automaticOutputStatuses });
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

        if (url.endsWith("/connection")) {
          return jsonResponse({
            session_id: "vs-1",
            connection_id: "conn-1",
            state: "connected",
            version: 1,
            updated_at: "2026-07-31T00:00:02Z",
          });
        }

        if (url.endsWith("/mode")) {
          let operationId: string | null = null;
          if (method === "POST") {
            modeRequests += 1;
            const body = JSON.parse(String(init?.body)) as {
              target_mode: "assistant" | "interpretation";
              operation_id: string;
            };
            activeMode = body.target_mode;
            operationId = body.operation_id;
            modeGeneration += 1;
          }
          const state = {
            session_id: "vs-1",
            runtime_instance_id: "runtime-1",
            active_mode: activeMode,
            generation: modeGeneration,
            phase: "active",
            last_operation_id: operationId,
            updated_at: "2026-07-31T00:00:02Z",
          };
          return jsonResponse(
            method === "POST"
              ? {
                  operation_id: operationId,
                  status: "applied",
                  state,
                }
              : state,
          );
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
          endRequests += 1;
          if (endRequestGate) return endRequestGate;
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
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
    vi.clearAllMocks();
  });

  it("starts with one primary voice entry point and a settings icon", () => {
    render(<VoiceExperience />);

    expect(screen.getByText("lingow")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "开始对话" })).toBeVisible();
    expect(screen.getByRole("button", { name: "设置" })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /对话/ })).toHaveLength(1);
    expect(
      screen.getByText("轻触开启助手"),
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

  it("renders assistant.reply text received on the shared DataChannel", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(startRequests).toBe(1));

    dataMessageHandler?.({
      type: "assistant.reply",
      id: "reply-1",
      turn_id: "turn-1",
      text: "我可以帮你查找路线。",
      language: "zh-CN",
    });

    expect(await screen.findByText("我可以帮你查找路线。"))
      .toBeInTheDocument();
  });

  it("settles the matching ASR partial when an assistant reply arrives", async () => {
    vi.stubEnv("NEXT_PUBLIC_LINGOW_INITIAL_MODE", "interpretation");
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());

    dataMessageHandler?.({
      type: "asr.partial", event_version: 1, session_id: "vs-1", turn_id: "turn-assistant-partial-1",
      text: "临时识别文本", occurred_at: "2026-08-18T01:02:03Z",
    });
    expect(await screen.findByText("临时识别文本")).toBeInTheDocument();

    dataMessageHandler?.({
      type: "assistant.reply", id: "reply-1", turn_id: "turn-assistant-partial-1", text: "助手最终回复", language: "zh-CN",
    });
    await waitFor(() => {
      expect(screen.queryByLabelText("临时识别结果")).not.toBeInTheDocument();
      expect(screen.getByText("助手已回复")).toBeInTheDocument();
    });

    dataMessageHandler?.({
      type: "asr.partial", event_version: 1, session_id: "vs-1", turn_id: "turn-assistant-partial-1",
      text: "迟到文本", occurred_at: "2026-08-18T01:02:04Z",
    });
    expect(screen.queryByText("迟到文本")).not.toBeInTheDocument();
  });

  it("replaces transient ASR text and ignores a late partial after final", async () => {
    vi.stubEnv("NEXT_PUBLIC_LINGOW_INITIAL_MODE", "interpretation");
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));
    await waitFor(() => expect(startRequests).toBe(1));

    dataMessageHandler?.({
      type: "asr.partial",
      event_version: 1,
      session_id: "vs-1",
      turn_id: "turn-partial-1",
      text: "正在识别的原文",
      occurred_at: "2026-08-18T01:02:03Z",
    });
    expect(await screen.findByText("正在识别的原文")).toBeInTheDocument();

    dataMessageHandler?.({
      type: "asr.partial",
      event_version: 1,
      session_id: "vs-1",
      turn_id: "turn-partial-1",
      text: "正在识别的完整原文",
      occurred_at: "2026-08-18T01:02:04Z",
    });
    expect(await screen.findByText("正在识别的完整原文")).toBeInTheDocument();
    expect(screen.queryByText("正在识别的原文")).not.toBeInTheDocument();

    dataMessageHandler?.({
      type: "translation.final",
      turn_id: "turn-partial-1",
      source_text: "正在识别的完整原文",
      translated_text: "Final translation",
      source_language: "zh-CN",
      target_language: "en-US",
    });
    await waitFor(() => {
      expect(screen.queryByLabelText("临时识别结果")).not.toBeInTheDocument();
      expect(screen.getByText("Final translation")).toBeInTheDocument();
    });

    dataMessageHandler?.({
      type: "asr.partial",
      event_version: 1,
      session_id: "vs-1",
      turn_id: "turn-partial-1",
      text: "不应显示的迟到文本",
      occurred_at: "2026-08-18T01:02:05Z",
    });
    expect(screen.queryByText("不应显示的迟到文本")).not.toBeInTheDocument();
  });

  it("starts new Web sessions in assistant mode", async () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));

    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());
    expect(startInitialModes).toEqual(["assistant"]);
  });

  it("uses interpretation labels and request mode when rollback is configured", async () => {
    vi.stubEnv("NEXT_PUBLIC_LINGOW_INITIAL_MODE", "interpretation");
    render(<VoiceExperience />);

    expect(screen.getByRole("button", { name: "开始翻译" })).toBeVisible();
    expect(screen.queryByText(/开启助手/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));

    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());
    expect(startInitialModes).toEqual(["interpretation"]);
    expect(screen.getByRole("button", { name: "停止翻译" })).toBeVisible();
  });

  it("shows account status immediately above about in settings", async () => {
    const onLogout = vi.fn();
    render(<VoiceExperience onLogout={onLogout} />);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));

    expect(screen.getByRole("dialog", { name: "设置" })).toBeInTheDocument();
    expect(
      screen.getByRole("listbox", { name: "设置选项" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "语言配置" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "联调会话" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "历史会话" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "用量管理" })).toBeInTheDocument();
    const accountOption = screen.getByRole("option", { name: "登录状态" });
    const aboutOption = screen.getByRole("option", { name: "关于" });
    expect(accountOption.compareDocumentPosition(aboutOption)).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    );
    expect(screen.getByText("01")).toBeInTheDocument();
    expect(screen.getByText("07")).toBeInTheDocument();

    fireEvent.click(accountOption);
    expect(await screen.findByText("手机号验证账户")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "退出登录" }));
    expect(onLogout).toHaveBeenCalledTimes(1);
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

  it("selects single broadcast mode and persists the preference", async () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const singleMode = screen.getByRole("button", { name: "单向播报" });
    await waitFor(() => expect(singleMode).not.toBeDisabled());
    fireEvent.click(singleMode);

    expect(singleMode).toHaveAttribute("aria-pressed", "true");
    expect(JSON.parse(localStorage.getItem("lingow-voice-config-v2") ?? "{}")).toMatchObject({
      outputMode: "single",
    });
    expect(
      screen.getByText(/反向译文自动投递，并保留 Final Turn/),
    ).toBeInTheDocument();
    expect(screen.queryByText("单向播报 · 中文 → English")).toBeNull();
  });

  it("swaps the single broadcast direction before starting a session", async () => {
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const singleMode = screen.getByRole("button", { name: "单向播报" });
    await waitFor(() => expect(singleMode).not.toBeDisabled());
    fireEvent.click(singleMode);
    fireEvent.click(screen.getByRole("button", { name: "交换播报方向" }));

    expect(screen.queryByText("单向播报 · English → 中文")).toBeNull();
    expect(JSON.parse(localStorage.getItem("lingow-voice-config-v2") ?? "{}")).toMatchObject({
      sourceLanguage: "en-US",
      targetLanguage: "zh-CN",
      outputMode: "single",
    });

    fireEvent.click(screen.getByRole("button", { name: "关闭设置" }));
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());

    expect(languageConfigRequests.at(-1)?.output_routes).toEqual([
      { target_language: "zh-CN", tts_enabled: true, delivery_enabled: false },
      { target_language: "en-US", tts_enabled: false, delivery_enabled: true },
    ]);
  });

  it("starts a new session with bidirectional broadcast when no target is ready", async () => {
    automaticDeliveryReady = false;
    localStorage.setItem(
      "lingow-voice-config-v2",
      JSON.stringify({
        sourceLanguage: "zh-CN",
        targetLanguage: "en-US",
        outputMode: "single",
      }),
    );
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());

    expect(screen.queryByText("双向播报 · 中文 ⇄ English")).toBeNull();
    expect(JSON.parse(localStorage.getItem("lingow-voice-config-v2") ?? "{}")).toMatchObject({
      outputMode: "bidirectional",
    });
    expect(languageConfigRequests.at(-1)?.output_routes).toEqual([
      { target_language: "en-US", tts_enabled: true, delivery_enabled: false },
      { target_language: "zh-CN", tts_enabled: true, delivery_enabled: false },
    ]);
  });

  it("shows fallback playback while automatic delivery is recovering", async () => {
    automaticOutputStatuses = [
      {
        turn_id: "turn-1",
        status: "fallback_pending",
        updated_at: "2026-07-31T00:00:04Z",
      },
    ];
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));

    expect(
      await screen.findByText("自动投递全部失败，正在补播反向译文。"),
    ).toBeInTheDocument();
  });

  it("refreshes the authoritative output routes after automatic recovery", async () => {
    automaticOutputStatuses = [
      {
        turn_id: "turn-1",
        status: "restored",
        updated_at: "2026-07-31T00:00:05Z",
      },
    ];
    localStorage.setItem(
      "lingow-voice-config-v2",
      JSON.stringify({
        sourceLanguage: "zh-CN",
        targetLanguage: "en-US",
        outputMode: "single",
      }),
    );
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));

    expect(
      await screen.findByText("自动投递失败，已恢复双向播报。"),
    ).toBeInTheDocument();
    await waitFor(() => {
      expect(JSON.parse(localStorage.getItem("lingow-voice-config-v2") ?? "{}")).toMatchObject({
        outputMode: "bidirectional",
      });
    });
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
    vi.stubEnv("NEXT_PUBLIC_LINGOW_INITIAL_MODE", "interpretation");
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

  it("shows runtime connection/mode state and sends a typed mode command", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));

    await waitFor(() => {
      expect(screen.getByText(/连接：connected/)).toBeInTheDocument();
      expect(screen.getByText(/Mode：assistant/)).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole("button", { name: "同声传译" }));
    await waitFor(() => {
      expect(modeRequests).toBe(1);
      expect(screen.getByText(/Mode：interpretation/)).toBeInTheDocument();
    });
  });

  it("sends a wake signal and applies only the matching semantic command result", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));

    await waitFor(() => {
      expect(screen.getByText(/Mode：assistant/)).toBeInTheDocument();
      expect(wakeHandler).toBeDefined();
    });

    wakeHandler?.("小灵小灵");
    await waitFor(() => expect(wakeWordSend).toHaveBeenCalledTimes(1));
    const firstSignal = JSON.parse(String(wakeWordSend.mock.calls[0]?.[0])) as {
      signal_id: string;
    };

    wakeHandler?.("小灵小灵");
    await waitFor(() => expect(wakeWordSend).toHaveBeenCalledTimes(2));
    const currentSignal = JSON.parse(String(wakeWordSend.mock.calls[1]?.[0])) as {
      signal_id: string;
    };
    expect(currentSignal.signal_id).not.toBe(firstSignal.signal_id);
    expect(screen.getByText(/正在听取指令/)).toBeInTheDocument();

    dataMessageHandler?.({
      type: "command.result",
      event_version: 1,
      command_id: firstSignal.signal_id,
      session_id: "vs-1",
      runtime_instance_id: "rt-1",
      generation: 2,
      status: "applied",
      action: "activate_mode",
      target_mode: "interpretation",
      message: "不应显示的迟到结果",
      occurred_at: "2026-08-13T10:00:00Z",
    });
    expect(screen.queryByText("不应显示的迟到结果")).not.toBeInTheDocument();

    activeMode = "interpretation";
    modeGeneration = 2;
    dataMessageHandler?.({
      type: "command.result",
      event_version: 1,
      command_id: currentSignal.signal_id,
      session_id: "vs-1",
      runtime_instance_id: "rt-1",
      generation: 2,
      status: "applied",
      action: "activate_mode",
      target_mode: "interpretation",
      message: "已进入同声传译模式",
      occurred_at: "2026-08-13T10:00:01Z",
    });

    await waitFor(() => {
      expect(screen.getByText("已进入同声传译模式")).toBeInTheDocument();
      expect(screen.getByText(/Mode：interpretation/)).toBeInTheDocument();
    });
  });

  it("gates WebRTC uplink to one bounded turn in wake-word mode", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));

    const continuous = await screen.findByRole("button", { name: "常驻模式" });
    const wakeWord = screen.getByRole("button", { name: "唤醒词模式" });
    expect(continuous).toHaveAttribute("aria-pressed", "true");
    expect(uplinkTrack.enabled).toBe(true);

    fireEvent.click(wakeWord);
    expect(wakeWord).toHaveAttribute("aria-pressed", "true");
    expect(uplinkTrack.enabled).toBe(false);
    expect(localStorage.getItem("lingow.voice.interaction-policy")).toBe("wake_word");

    wakeHandler?.("小灵小灵");
    await waitFor(() => expect(wakeWordSend).toHaveBeenCalledTimes(1));
    expect(uplinkTrack.enabled).toBe(true);
    const signal = JSON.parse(String(wakeWordSend.mock.calls[0]?.[0])) as {
      signal_id: string;
    };

    dataMessageHandler?.({
      type: "command.result",
      event_version: 1,
      command_id: signal.signal_id,
      session_id: "vs-1",
      runtime_instance_id: "rt-1",
      generation: 1,
      status: "unchanged",
      action: "assistant_query",
      target_mode: "assistant",
      message: "助手请求已处理",
      occurred_at: "2026-08-13T10:00:01Z",
    });
    await waitFor(() => expect(uplinkTrack.enabled).toBe(false));
  });

  it("forces continuous uplink in interpretation and restores wake-word policy after voice exit", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));

    const wakeWord = await screen.findByRole("button", { name: "唤醒词模式" });
    fireEvent.click(wakeWord);
    expect(uplinkTrack.enabled).toBe(false);

    fireEvent.click(screen.getByRole("button", { name: "同声传译" }));
    await waitFor(() => {
      expect(screen.getByText(/Mode：interpretation/)).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "常驻模式" })).toHaveAttribute(
        "aria-pressed",
        "true",
      );
      expect(wakeWord).toBeDisabled();
      expect(uplinkTrack.enabled).toBe(true);
    });

    wakeHandler?.("小灵小灵");
    await waitFor(() => expect(wakeWordSend).toHaveBeenCalledTimes(1));
    const signal = JSON.parse(String(wakeWordSend.mock.calls[0]?.[0])) as {
      signal_id: string;
    };
    activeMode = "assistant";
    modeGeneration += 1;
    dataMessageHandler?.({
      type: "command.result",
      event_version: 1,
      command_id: signal.signal_id,
      session_id: "vs-1",
      runtime_instance_id: "runtime-1",
      generation: modeGeneration,
      status: "applied",
      action: "return_to_assistant",
      target_mode: "assistant",
      message: "已返回通用助手模式",
      occurred_at: "2026-08-13T10:00:02Z",
    });

    await waitFor(() => {
      expect(screen.getByText(/Mode：assistant/)).toBeInTheDocument();
      expect(wakeWord).toBeEnabled();
      expect(wakeWord).toHaveAttribute("aria-pressed", "true");
      expect(uplinkTrack.enabled).toBe(false);
    });
  });

  it("closes a wake-word uplink turn when no command result arrives", async () => {
    localStorage.setItem("lingow.voice.interaction-policy", "wake_word");
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));

    await waitFor(() => expect(uplinkTrack.enabled).toBe(false));
    vi.useFakeTimers();
    await act(async () => {
      wakeHandler?.("小灵小灵");
    });
    expect(uplinkTrack.enabled).toBe(true);
    await act(async () => {
      vi.advanceTimersByTime(15_000);
    });

    expect(uplinkTrack.enabled).toBe(false);
    expect(
      screen.getByText("本轮唤醒已超时，麦克风上行已关闭，请再次说「小灵小灵」。"),
    ).toBeInTheDocument();
  });

  it("uses the runtime mode for controls and output after switching to interpretation", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => {
      expect(screen.getByText(/Mode：assistant/)).toBeInTheDocument();
    });

    dataMessageHandler?.({
      type: "assistant.reply",
      id: "reply-before-interpretation",
      turn_id: "turn-before-interpretation",
      text: "这是切换前的助手回复。",
      language: "zh-CN",
    });
    expect(await screen.findByText("这是切换前的助手回复。")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "同声传译" }));

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "停止翻译" })).toBeVisible();
      expect(screen.queryByText("这是切换前的助手回复。")).not.toBeInTheDocument();
      expect(
        screen.getByText("Hello, how can I get to the main venue?"),
      ).toBeInTheDocument();
    });
  });

  it("uses the runtime mode for controls and output after switching to assistant", async () => {
    vi.stubEnv("NEXT_PUBLIC_LINGOW_INITIAL_MODE", "interpretation");
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始翻译" }));
    await waitFor(() => {
      expect(screen.getByText(/Mode：interpretation/)).toBeInTheDocument();
      expect(
        screen.getByText("Hello, how can I get to the main venue?"),
      ).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "AI 助手" }));

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "停止对话" })).toBeVisible();
      expect(
        screen.queryByText("Hello, how can I get to the main venue?"),
      ).not.toBeInTheDocument();
    });

    dataMessageHandler?.({
      type: "assistant.reply",
      id: "reply-after-assistant",
      turn_id: "turn-after-assistant",
      text: "切换到助手后显示这条回复。",
      language: "zh-CN",
    });

    await waitFor(() => {
      expect(screen.getByText("切换到助手后显示这条回复。")).toBeInTheDocument();
      expect(
        screen.queryByText("Hello, how can I get to the main venue?"),
      ).not.toBeInTheDocument();
    });
  });

  it("refreshes the language config version after a concurrent update", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    const singleMode = screen.getByRole("button", { name: "单向播报" });
    await waitFor(() => expect(singleMode).not.toBeDisabled());

    conflictNextLanguageConfig = true;
    fireEvent.click(singleMode);
    await waitFor(() => {
      expect(screen.getByText(/当前会话应用失败，已恢复上一次配置/)).toBeInTheDocument();
      expect(singleMode).toHaveAttribute("aria-pressed", "false");
      expect(screen.getByRole("button", { name: "双向播报" })).toHaveAttribute(
        "aria-pressed",
        "true",
      );
      expect(JSON.parse(localStorage.getItem("lingow-voice-config-v2") ?? "{}")).toMatchObject({
        outputMode: "bidirectional",
      });
    });

    fireEvent.click(screen.getByRole("button", { name: "双向播报" }));
    await waitFor(() => expect(screen.getByText(/已应用到当前会话/)).toBeInTheDocument());

    expect(languageConfigExpectedVersions).toEqual([undefined, 1, 2]);
  });

  it("opens the complete history from the newest subtitle", async () => {
    vi.stubEnv("NEXT_PUBLIC_LINGOW_INITIAL_MODE", "interpretation");
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
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));

    await waitFor(() => {
      expect(screen.getByText("正在聆听")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "停止对话" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "开始对话" })).toBeVisible();
    });
    expect(
      screen.getByText("轻触开启助手"),
    ).toBeInTheDocument();
    expect(closeWebRTC).toHaveBeenCalled();
  });

  it("stops realtime before closing WebRTC media", async () => {
    const pendingEnd = deferred<Response>();
    endRequestGate = pendingEnd.promise;

    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "停止对话" }));
    await waitFor(() => expect(endRequests).toBe(1));
    expect(closeWebRTC).not.toHaveBeenCalled();

    pendingEnd.resolve(
      jsonResponse({
        id: "vs-1",
        account_id: "acc-1",
        status: "ended",
        created_at: "2026-07-31T00:00:00Z",
        ended_at: "2026-07-31T00:00:10Z",
      }),
    );

    await waitFor(() => expect(closeWebRTC).toHaveBeenCalledTimes(1));
  });

  it("cleans up WebRTC media when ending the session times out", async () => {
    const pendingEnd = deferred<Response>();
    endRequestGate = pendingEnd.promise;

    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());

    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "停止对话" }));
    expect(endRequests).toBe(1);
    expect(closeWebRTC).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(END_REQUEST_TIMEOUT_MS);
    });

    expect(closeWebRTC).toHaveBeenCalledTimes(1);
    expect(
      screen.getByText("结束请求超时，服务端可能仍在处理，请稍后确认会话状态。"),
    ).toBeInTheDocument();

    pendingEnd.resolve(
      jsonResponse({
        id: "vs-1",
        account_id: "acc-1",
        status: "ended",
        created_at: "2026-07-31T00:00:00Z",
        ended_at: "2026-07-31T00:00:10Z",
      }),
    );
    await act(async () => {
      await pendingEnd.promise;
    });
  });

  it("keeps the UI idle and closes a session created after startup cancellation", async () => {
    const pendingSession = deferred<Response>();
    sessionCreationGate = pendingSession.promise;
    render(<VoiceExperience />);

    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(createdSessions).toBe(1));
    fireEvent.click(screen.getByRole("button", { name: "停止对话" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "开始对话" })).toBeVisible();
    });

    pendingSession.resolve(
      jsonResponse(
        {
          id: "vs-cancelled",
          account_id: "acc-1",
          status: "created",
          created_at: "2026-07-31T00:00:00Z",
        },
        201,
      ),
    );

    await waitFor(() => expect(endRequests).toBe(1));
    expect(startRequests).toBe(0);
    expect(closeWebRTC).not.toHaveBeenCalled();
    expect(screen.getByText("轻触开启助手")).toBeInTheDocument();
  });

  it("reuses the same registered account for later sessions", async () => {
    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "停止对话" }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "开始对话" })).toBeVisible(),
    );
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(createdSessions).toBe(2));

    expect(anonymousRequests).toBe(0);
  });

  it("returns to a fresh start after a failed session startup", async () => {
    failFirstStart = true;

    render(<VoiceExperience />);
    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "开始对话" })).toBeVisible();
    });

    fireEvent.click(screen.getByRole("button", { name: "开始对话" }));
    await waitFor(() => expect(screen.getByText("正在聆听")).toBeInTheDocument());
    expect(createdSessions).toBe(2);
    expect(startRequests).toBe(3);
  });
});
