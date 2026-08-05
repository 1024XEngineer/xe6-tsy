"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";

import {
  createLanguageConfig,
  createVoiceSession,
  endVoiceSession,
  getVoiceSessionState,
  listSessionTurns,
  mintRealtimeTicket,
  startVoiceSession,
  type RuntimeState,
  type VoiceTurn,
} from "../lib/lingow-api";
import { getOrCreateAuthSession } from "../lib/auth-session";
import {
  DEFAULT_VOICE_CONFIG,
  formatActivePair,
  languageLabel,
  type VoiceSessionConfig,
} from "../lib/languages";
import { parseTranslationFinal } from "../lib/translation-events";
import { enqueueTTSAudio, parseTTSAudioEvent } from "../lib/tts-playback";
import {
  loadVoiceConfig,
  normalizeVoiceConfig,
  saveVoiceConfig,
} from "../lib/voice-settings";
import {
  openWebRTCSession,
  type WebRTCSessionHandles,
} from "../lib/webrtc-session";
import {
  WakeWordListener,
  type WakeListenerStatus,
} from "../lib/wake-word/wake-listener";
import {
  initialSession,
  sessionReducer,
  type TranslationTurn,
} from "../model/session";

const POLL_INTERVAL_MS = 1200;
const TTS_INPUT_RESUME_DELAY_MS = 300;

export type SessionDebugInfo = {
  accountId: string | null;
  sessionId: string | null;
  runtimeState: RuntimeState | null;
  lastError: string | null;
  wakeStatus: WakeListenerStatus;
};

function mapRuntimeToStatus(runtime: RuntimeState | null): string {
  switch (runtime) {
    case "starting":
      return "正在启动会话";
    case "listening":
      return "正在聆听";
    case "asr_processing":
      return "正在识别";
    case "translating":
      return "正在翻译";
    case "tts_processing":
      return "正在合成语音";
    case "playing":
      return "正在播放译音";
    case "stopping":
      return "正在结束";
    case "failed":
      return "会话失败";
    case "stopped":
      return "已停止";
    default:
      return "会话进行中";
  }
}

function mapRuntimePhase(
  runtime: RuntimeState | null,
): "processing" | "playing" | "active" {
  if (runtime === "asr_processing" || runtime === "translating") {
    return "processing";
  }
  if (runtime === "tts_processing" || runtime === "playing") {
    return "playing";
  }
  return "active";
}

function toTranslationTurn(turn: VoiceTurn): TranslationTurn {
  return {
    id: turn.id,
    sourceLanguage: languageLabel(turn.source_language),
    targetLanguage: languageLabel(turn.target_language),
    source: turn.source_text,
    translation: turn.translated_text,
  };
}

function errorMessage(error: unknown, fallback: string): string {
  const message = error instanceof Error ? error.message : fallback;
  if (
    message.includes("voice session dependency is not implemented") ||
    message.includes("[not_implemented]")
  ) {
    return (
      `${message} — 请在 xe6-tsy/.env 设置 LINGOW_SESSION_RUNTIME=enabled，` +
      `并配置 REALTIME_BASE_URL 与 REALTIME_TICKET_SECRET（≥32 字节），然后重启 API。` +
      `详见 CONFIG.md。`
    );
  }
  if (
    message.includes("ECONNREFUSED") &&
    (message.includes("8090") || message.includes("realtime"))
  ) {
    return (
      "realtime :8090 未启动（ECONNREFUSED）。请先运行 xe6-tsy/start-realtime.bat。"
    );
  }
  if (message.includes("ECONNREFUSED") && message.includes("8080")) {
    return "API :8080 未启动（ECONNREFUSED）。请先运行 xe6-tsy/start-api.bat。";
  }
  return message;
}

function idleHintForWake(status: WakeListenerStatus): string | null {
  switch (status) {
    case "requesting_mic":
      return "请允许麦克风，以便唤醒词与传译共用同一输入。";
    case "loading_model":
      return "正在加载本地唤醒模型（首次约十几 MB）…";
    case "listening":
      return "可说「小灵，开始翻译」或轻触开始。";
    case "error":
      return "唤醒词不可用，仍可用按钮开始传译。";
    default:
      return null;
  }
}

export function useVoiceSession() {
  const [state, dispatch] = useReducer(sessionReducer, initialSession);
  const [statusMessage, setStatusMessage] = useState("正在准备麦克风");
  const [hintMessage, setHintMessage] = useState<string | null>(null);
  const [voiceConfig, setVoiceConfig] = useState<VoiceSessionConfig>(() =>
    loadVoiceConfig(DEFAULT_VOICE_CONFIG),
  );
  const [wakeStatus, setWakeStatus] = useState<WakeListenerStatus>("idle");
  const [debug, setDebug] = useState<SessionDebugInfo>({
    accountId: null,
    sessionId: null,
    runtimeState: null,
    lastError: null,
    wakeStatus: "idle",
  });

  const configRef = useRef<VoiceSessionConfig>(voiceConfig);
  const runningRef = useRef(false);
  const accessTokenRef = useRef<string | null>(null);
  const accountIdRef = useRef<string | null>(null);
  const sessionIdRef = useRef<string | null>(null);
  const webrtcRef = useRef<WebRTCSessionHandles | null>(null);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const startAbortRef = useRef<AbortController | null>(null);
  const wakeRef = useRef<WakeWordListener | null>(null);
  const startRef = useRef<() => Promise<void>>(async () => undefined);
  const endRef = useRef<() => Promise<void>>(async () => undefined);

  const updateConfig = useCallback((next: VoiceSessionConfig) => {
    const normalized = normalizeVoiceConfig(next);
    configRef.current = normalized;
    setVoiceConfig(normalized);
    saveVoiceConfig(normalized);
  }, []);

  const stopPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearInterval(pollTimerRef.current);
      pollTimerRef.current = null;
    }
  }, []);

  const cleanupMedia = useCallback(() => {
    webrtcRef.current?.close();
    webrtcRef.current = null;
  }, []);

  const syncTurnsAndState = useCallback(async () => {
    const token = accessTokenRef.current;
    const sessionId = sessionIdRef.current;
    if (!token || !sessionId || !runningRef.current) return;

    try {
      const [turnsPage, snapshot] = await Promise.all([
        listSessionTurns(token, sessionId),
        getVoiceSessionState(token, sessionId),
      ]);

      dispatch({
        type: "SET_TURNS",
        turns: turnsPage.items.map(toTranslationTurn),
      });

      const phase = mapRuntimePhase(snapshot.runtime_state);
      if (phase === "processing") dispatch({ type: "PROCESSING" });
      else if (phase === "playing") dispatch({ type: "PLAYING" });
      else dispatch({ type: "ACTIVATE" });

      setStatusMessage(mapRuntimeToStatus(snapshot.runtime_state));
      setDebug((prev) => ({
        ...prev,
        runtimeState: snapshot.runtime_state,
        lastError: snapshot.last_error_code,
      }));

      if (snapshot.runtime_state === "failed") {
        const code = snapshot.last_error_code
          ? `（${snapshot.last_error_code}）`
          : "";
        setHintMessage(
          `实时管道已失败${code}。信令/建连可能已成功，但 ASR→翻译 worker 退出了；请重启 start-local 后再试，并确认说话时有声音输入。`,
        );
      } else if (snapshot.last_error_code) {
        setHintMessage(`last_error_code: ${snapshot.last_error_code}`);
      }
    } catch (error) {
      setHintMessage(errorMessage(error, "轮询会话状态失败"));
    }
  }, []);

  const startPolling = useCallback(() => {
    stopPolling();
    void syncTurnsAndState();
    pollTimerRef.current = setInterval(() => {
      void syncTurnsAndState();
    }, POLL_INTERVAL_MS);
  }, [stopPolling, syncTurnsAndState]);

  const end = useCallback(async () => {
    runningRef.current = false;
    startAbortRef.current?.abort();
    startAbortRef.current = null;
    stopPolling();
    cleanupMedia();

    const token = accessTokenRef.current;
    const sessionId = sessionIdRef.current;
    if (token && sessionId) {
      try {
        await endVoiceSession(token, sessionId, "user_requested");
      } catch (error) {
        setHintMessage(errorMessage(error, "结束会话失败"));
      }
    }

    sessionIdRef.current = null;
    dispatch({ type: "END" });
    setStatusMessage(
      wakeRef.current?.getStatus() === "listening"
        ? "轻触或说「小灵，开始翻译」"
        : "轻触开始",
    );
    setHintMessage(idleHintForWake(wakeRef.current?.getStatus() ?? "idle"));
    setDebug((prev) => ({
      accountId: accountIdRef.current,
      sessionId: null,
      runtimeState: null,
      lastError: null,
      wakeStatus: prev.wakeStatus,
    }));
  }, [cleanupMedia, stopPolling]);

  const start = useCallback(async () => {
    if (runningRef.current) return;

    runningRef.current = true;
    const startAbort = new AbortController();
    startAbortRef.current = startAbort;
    dispatch({ type: "START" });
    setStatusMessage("正在匿名登录");
    setHintMessage("连接 xe6-tsy API…");
    setDebug((prev) => ({
      accountId: null,
      sessionId: null,
      runtimeState: null,
      lastError: null,
      wakeStatus: prev.wakeStatus,
    }));

    try {
      const auth = await getOrCreateAuthSession();
      accessTokenRef.current = auth.tokens.access_token;
      accountIdRef.current = auth.account.id;
      setDebug((prev) => ({ ...prev, accountId: auth.account.id }));
      setStatusMessage("正在创建会话");

      const session = await createVoiceSession(auth.tokens.access_token);
      sessionIdRef.current = session.id;
      setDebug((prev) => ({ ...prev, sessionId: session.id }));
      setHintMessage(`session: ${session.id}`);

      setStatusMessage("正在配置语言");
      await createLanguageConfig(
        auth.tokens.access_token,
        session.id,
        configRef.current,
      );
      setHintMessage(
        `${formatActivePair(configRef.current)} · ${session.id}`,
      );

      setStatusMessage("正在申请实时票据");
      const ticketResponse = await mintRealtimeTicket(
        auth.tokens.access_token,
        session.id,
      );
      const ticket = ticketResponse.ticket;

      let sessionStream: MediaStream | null = null;
      let ttsResumeTimer: ReturnType<typeof setTimeout> | null = null;
      const setMicrophoneInputEnabled = (enabled: boolean) => {
        if (sessionIdRef.current !== session.id) return;
        const stream = sessionStream;
        if (!stream) return;
        if (ttsResumeTimer) {
          clearTimeout(ttsResumeTimer);
          ttsResumeTimer = null;
        }
        if (!enabled) {
          for (const track of stream.getAudioTracks()) {
            track.enabled = false;
          }
          return;
        }
        ttsResumeTimer = setTimeout(() => {
          if (sessionIdRef.current !== session.id) return;
          for (const track of stream.getAudioTracks()) {
            track.enabled = true;
          }
          ttsResumeTimer = null;
        }, TTS_INPUT_RESUME_DELAY_MS);
      };

      setStatusMessage("正在建立 WebRTC");
      setHintMessage("复用已授权麦克风，交换 SDP/ICE。");
      const wakeTracks = wakeRef.current?.cloneAudioTracksForPeer() ?? [];
      try {
        webrtcRef.current = await openWebRTCSession({
          ticket,
          sessionId: session.id,
          audioTracks: wakeTracks.length > 0 ? wakeTracks : undefined,
          onDataMessage: (payload) => {
            const audio = parseTTSAudioEvent(payload);
            if (audio) {
              enqueueTTSAudio(audio, (playing) => {
                setMicrophoneInputEnabled(!playing);
              });
              return;
            }
            const event = parseTranslationFinal(payload);
            if (!event) return;
            dispatch({
              type: "ADD_TURN",
              turn: {
                id: event.turnId,
                sourceLanguage: languageLabel(event.sourceLanguage),
                targetLanguage: languageLabel(event.targetLanguage),
                source: event.sourceText,
                translation: event.translatedText,
              },
            });
          },
        });
        sessionStream = webrtcRef.current.localStream;
      } catch (webrtcError) {
        const detail = errorMessage(webrtcError, "WebRTC 信令失败");
        throw new Error(
          `WebRTC/realtime 信令失败：${detail} ` +
            `API 侧匿名登录/建会话/语言配置/ticket 已成功（session=${session.id}）。` +
            `请确认已用最新代码重启 start-local.bat（:8080 + :8090），` +
            `且 API 与 realtime 的 REALTIME_TICKET_SECRET 一致。`,
        );
      }

      setStatusMessage("正在启动传译");
      setHintMessage("WebRTC 已 connected，正在调用 API /start…");
      try {
        await startVoiceSession(
          auth.tokens.access_token,
          session.id,
          undefined,
          startAbort.signal,
        );
      } catch (startError) {
        const detail = errorMessage(startError, "启动失败");
        throw new Error(
          `API /start 失败：${detail} ` +
            `（session=${session.id}）。` +
            `webrtc_not_ready=ICE 尚未 connected；realtime_start_failed=realtime 管道 Start/Activate 失败（常见：进程未用最新代码重启、语言配置/ASR 输入无效）。` +
            `请确认 REALTIME_BASE_URL 指向 :8090。`,
        );
      }

      dispatch({ type: "ACTIVATE" });
      setStatusMessage("正在聆听");
      setHintMessage(
        `传译已开启 · ${formatActivePair(configRef.current)} · 可说「小灵，停止翻译」或轻触结束`,
      );
      startPolling();
    } catch (error) {
      if (startAbort.signal.aborted) return;
      const message = errorMessage(error, "无法启动会话");
      dispatch({ type: "ERROR", message });
      setStatusMessage("联调失败");
      setHintMessage(message);
      setDebug((prev) => ({
        ...prev,
        lastError: message,
        sessionId: sessionIdRef.current,
        accountId: accountIdRef.current,
      }));

      const failedSessionId = sessionIdRef.current;
      const failedAccessToken = accessTokenRef.current;
      cleanupMedia();
      stopPolling();
      sessionIdRef.current = null;
      runningRef.current = false;
      dispatch({ type: "END" });
      setStatusMessage("联调失败");
      setHintMessage(message);
      if (failedAccessToken && failedSessionId) {
        void endVoiceSession(
          failedAccessToken,
          failedSessionId,
          "operator_cancelled",
        ).catch(() => undefined);
      }
    } finally {
      if (startAbortRef.current === startAbort) {
        startAbortRef.current = null;
      }
    }
  }, [cleanupMedia, startPolling, stopPolling]);

  useEffect(() => {
    startRef.current = start;
    endRef.current = end;
  }, [start, end]);

  const toggle = useCallback(() => {
    if (runningRef.current || sessionIdRef.current) {
      void end();
      return;
    }
    void start();
  }, [end, start]);

  useEffect(() => {
    const listener = new WakeWordListener({
      onCommand: (command, keyword) => {
        if (command === "start") {
          if (runningRef.current || sessionIdRef.current) return;
          setHintMessage(`已识别「${keyword}」，正在开启传译…`);
          void startRef.current();
          return;
        }
        if (!runningRef.current && !sessionIdRef.current) return;
        setHintMessage(`已识别「${keyword}」，正在停止传译…`);
        void endRef.current();
      },
      onStatus: (status, detail) => {
        setWakeStatus(status);
        setDebug((prev) => ({ ...prev, wakeStatus: status }));
        if (runningRef.current) return;
        if (status === "listening") {
          setStatusMessage("轻触或说「小灵，开始翻译」");
          setHintMessage(idleHintForWake(status));
          return;
        }
        if (status === "error") {
          setStatusMessage("轻触开始");
          setHintMessage(detail ?? idleHintForWake(status));
          return;
        }
        if (status === "requesting_mic" || status === "loading_model") {
          setStatusMessage(
            status === "requesting_mic" ? "请允许麦克风" : "正在加载唤醒模型",
          );
          setHintMessage(detail ?? idleHintForWake(status));
        }
      },
    });
    wakeRef.current = listener;
    void listener.start().catch(() => {
      // Status callback already reports the error; button path remains available.
    });

    return () => {
      wakeRef.current = null;
      listener.stop();
    };
  }, []);

  useEffect(
    () => () => {
      runningRef.current = false;
      startAbortRef.current?.abort();
      startAbortRef.current = null;
      stopPolling();
      cleanupMedia();
    },
    [cleanupMedia, stopPolling],
  );

  return useMemo(
    () => ({
      state,
      latestTurn: state.turns.at(-1),
      statusMessage,
      hintMessage: hintMessage ?? state.notice,
      voiceConfig,
      updateConfig,
      debug,
      wakeStatus,
      toggle,
    }),
    [
      debug,
      hintMessage,
      state,
      statusMessage,
      toggle,
      updateConfig,
      voiceConfig,
      wakeStatus,
    ],
  );
}
