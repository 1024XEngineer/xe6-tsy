"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";

import {
  createLanguageConfig,
  createVoiceSession,
  endVoiceSession,
  getCurrentLanguageConfig,
  getVoiceSessionState,
  hasReadyAutomaticTarget,
  listAutomaticOutputStatus,
  listSessionTurns,
  mintRealtimeTicket,
  resolveVoiceInitialMode,
  RUNTIME_ERROR_TRANSLATION_REJECTED,
  startVoiceSession,
  type AutomaticOutputStatus,
  type RuntimeState,
  type VoiceInitialMode,
  type VoiceTurn,
} from "../lib/lingow-api";
import {
  getRealtimeModeState,
  switchRealtimeMode,
  type ModeStateSnapshot,
  type RealtimeConnectionState,
  type RealtimeMode,
} from "../lib/realtime-api";
import { getAuthSession } from "../lib/auth-session";
import {
  DEFAULT_VOICE_CONFIG,
  formatActivePair,
  languageLabel,
  type VoiceSessionConfig,
  voiceConfigFromLanguageConfig,
} from "../lib/languages";
import { ApiError, newIdempotencyKey } from "../lib/http";
import { parseASRPartial, parsePhraseSubtitle, parseTranslationFinal } from "../lib/translation-events";
import { ModeSnapshotTracker } from "../lib/realtime-state";
import { RealtimeTicketCache, withRealtimeTicket } from "../lib/realtime-ticket-cache";
import { parseAssistantReply } from "../lib/assistant-replies";
import {
  effectiveVoiceInteractionPolicy,
  loadVoiceInteractionPolicy,
  saveVoiceInteractionPolicy,
  shouldSuppressMicrophoneDuringTTS,
  type VoiceInteractionPolicy,
} from "../lib/interaction-policy";
import {
  parseCommandResult,
  type CommandResultEvent,
} from "../lib/command-results";
import { enqueueTTSAudio, parseTTSAudioEvent } from "../lib/tts-playback";
import { sendWakeWordDetectedSignal } from "../lib/wake-word-signal";
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
export const COMMAND_UPLINK_TIMEOUT_MS = 15_000;

export type SessionDebugInfo = {
  accountId: string | null;
  sessionId: string | null;
  runtimeState: RuntimeState | null;
  connectionState: RealtimeConnectionState | null;
  modeState: ModeStateSnapshot | null;
  modeCommandPending: boolean;
  lastError: string | null;
  wakeStatus: WakeListenerStatus;
};

export type ConfigSyncStatus = "idle" | "saving" | "applied" | "failed";

export type CommandFeedback = {
  commandId: string;
  status: "listening" | CommandResultEvent["status"];
  message: string;
};

function automaticOutputStatusMessage(
  status: AutomaticOutputStatus["status"],
): string | null {
  switch (status) {
    case "fallback_pending":
      return "自动投递全部失败，正在补播反向译文。";
    case "fallback_played":
      return "反向译文已补播，正在恢复双向播报。";
    case "restored":
      return "自动投递失败，已恢复双向播报。";
    default:
      return null;
  }
}

function mapRuntimeToStatus(runtime: RuntimeState | null, mode: VoiceInitialMode): string {
  switch (runtime) {
    case "starting":
      return "正在启动会话";
    case "listening":
      return "正在聆听";
    case "asr_processing":
      return "正在识别";
    case "translating":
      return "正在翻译";
    case "assistant_processing":
      return "助手正在思考";
    case "tts_processing":
      return mode === "assistant" ? "正在准备回复" : "正在合成语音";
    case "playing":
      return mode === "assistant" ? "正在播放回复" : "正在播放译音";
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
  if (
    runtime === "asr_processing" ||
    runtime === "translating" ||
    runtime === "assistant_processing"
  ) {
    return "processing";
  }
  if (runtime === "tts_processing" || runtime === "playing") {
    return "playing";
  }
  return "active";
}

function mapRuntimeFailureHint(code: string | null): string {
  if (code === RUNTIME_ERROR_TRANSLATION_REJECTED) {
    return "译文模型出现了意外行为，已拒绝本次服务。请结束会话后重试。";
  }
  const codePart = code ? `（${code}）` : "";
  return `实时管道已失败${codePart}。请在 realtime-audio 日志中按当前 session ID 查找「realtime pipeline worker failed」以确认具体失败阶段，重启后再试。`;
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

function idleHintForWake(status: WakeListenerStatus, mode: VoiceInitialMode): string | null {
  switch (status) {
    case "requesting_mic":
      return `请允许麦克风，以便唤醒词与${mode === "assistant" ? "助手" : "传译"}共用同一输入。`;
    case "loading_model":
      return "正在加载本地唤醒模型（首次约十几 MB）…";
    case "listening":
      return `轻触开始${mode === "assistant" ? "助手" : "传译"}；会话中说「小灵小灵」后可下达语义指令。`;
    case "error":
      return `唤醒词不可用，仍可用按钮开始${mode === "assistant" ? "对话" : "传译"}。`;
    default:
      return null;
  }
}

function activeHintForWake(status: WakeListenerStatus): string | null {
  switch (status) {
    case "requesting_mic":
      return "正在申请麦克风权限，唤醒词暂不可用。";
    case "loading_model":
      return "正在加载本地唤醒模型，加载完成后可说「小灵小灵」。";
    case "error":
      return "唤醒词启动失败，当前只能使用页面按钮切换模式。";
    default:
      return null;
  }
}

export function useVoiceSession() {
  const [state, dispatch] = useReducer(sessionReducer, initialSession);
  const initialMode = resolveVoiceInitialMode();
  const [statusMessage, setStatusMessage] = useState(
    initialMode === "assistant" ? "轻触开启助手" : "轻触开启传译",
  );
  const [hintMessage, setHintMessage] = useState<string | null>(null);
  const [automaticOutputMessage, setAutomaticOutputMessage] = useState<string | null>(null);
  const [configSyncStatus, setConfigSyncStatus] = useState<ConfigSyncStatus>("idle");
  const [voiceConfig, setVoiceConfig] = useState<VoiceSessionConfig>(() =>
    loadVoiceConfig(DEFAULT_VOICE_CONFIG),
  );
  const [wakeStatus, setWakeStatus] = useState<WakeListenerStatus>("idle");
  const [interactionPolicy, setInteractionPolicyState] =
    useState<VoiceInteractionPolicy>(loadVoiceInteractionPolicy);
  const [commandFeedback, setCommandFeedback] = useState<CommandFeedback | null>(null);
  const [debug, setDebug] = useState<SessionDebugInfo>({
    accountId: null,
    sessionId: null,
    runtimeState: null,
    connectionState: null,
    modeState: null,
    modeCommandPending: false,
    lastError: null,
    wakeStatus: "idle",
  });
  const activeMode = debug.modeState?.active_mode ?? initialMode;
  const effectiveInteractionPolicy = effectiveVoiceInteractionPolicy(
    activeMode,
    interactionPolicy,
  );

  const configRef = useRef<VoiceSessionConfig>(voiceConfig);
  const runningRef = useRef(false);
  const accessTokenRef = useRef<string | null>(null);
  const accountIdRef = useRef<string | null>(null);
  const sessionIdRef = useRef<string | null>(null);
  const webrtcRef = useRef<WebRTCSessionHandles | null>(null);
  const pollTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const startAbortRef = useRef<AbortController | null>(null);
  const realtimeTicketCacheRef = useRef<RealtimeTicketCache | null>(null);
  useEffect(() => {
    const cache = new RealtimeTicketCache({
      mint: async () => {
        const token = accessTokenRef.current;
        const sessionId = sessionIdRef.current;
        if (!token || !sessionId || !runningRef.current) {
          throw new Error("当前会话不能续签 realtime ticket");
        }
        const ticket = await mintRealtimeTicket(token, sessionId);
        if (sessionIdRef.current !== sessionId || !runningRef.current) {
          throw new Error("realtime ticket 所属会话已结束");
        }
        return ticket;
      },
    });
    realtimeTicketCacheRef.current = cache;
    return () => {
      cache.clear();
      if (realtimeTicketCacheRef.current === cache) {
        realtimeTicketCacheRef.current = null;
      }
    };
  }, []);
  const modeStateRef = useRef<ModeStateSnapshot | null>(null);
  const modeSnapshotTrackerRef = useRef(new ModeSnapshotTracker());
  const modeOperationRef = useRef<string | null>(null);
  const activeLanguageConfigVersionRef = useRef<number | null>(null);
  const lastAppliedVoiceConfigRef = useRef<VoiceSessionConfig>(voiceConfig);
  const activeConfigUpdateChainRef = useRef<Promise<void>>(Promise.resolve());
  const configRevisionRef = useRef(0);
  const pendingConfigUpdatesRef = useRef(0);
  const latestAutomaticOutputStatusRef = useRef<string | null>(null);
  const wakeRef = useRef<WakeWordListener | null>(null);
  const activeCommandIdRef = useRef<string | null>(null);
  const interactionPolicyRef = useRef<VoiceInteractionPolicy>(interactionPolicy);
  const setUplinkEnabledRef = useRef<(enabled: boolean) => void>(() => undefined);
  const openCommandUplinkRef = useRef<() => void>(() => undefined);
  const commandUplinkTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const settledPartialTurnsRef = useRef(new Set<string>());
  const activePartialTurnRef = useRef<string | null>(null);
  const partialTextByTurnRef = useRef(new Map<string, string>());
  const startRef = useRef<() => Promise<void>>(async () => undefined);
  const endRef = useRef<() => Promise<void>>(async () => undefined);

  const applyModeSnapshot = useCallback((snapshot: ModeStateSnapshot): boolean => {
    if (!modeSnapshotTrackerRef.current.observe(snapshot)) return false;
    const previous = modeStateRef.current;
    const runtimeChanged = Boolean(
      previous && previous.runtime_instance_id !== snapshot.runtime_instance_id,
    );
    modeStateRef.current = snapshot;
    const modeChanged = !previous || previous.active_mode !== snapshot.active_mode;
    if (runtimeChanged) modeOperationRef.current = null;

    if (modeChanged || runtimeChanged) {
      const activeTurn = activePartialTurnRef.current;
      if (activeTurn) settledPartialTurnsRef.current.add(activeTurn);
      activePartialTurnRef.current = null;
      dispatch({ type: "CLEAR_ASR_PARTIAL" });
    }
    setDebug((prev) => ({
      ...prev,
      modeState: snapshot,
      modeCommandPending: runtimeChanged ? false : prev.modeCommandPending,
    }));
    if (runtimeChanged) {
      setHintMessage("realtime runtime 已更换，模式快照已刷新，请重新发送模式命令。");
    }
    if (runningRef.current && (modeChanged || runtimeChanged)) {
      if (commandUplinkTimerRef.current) {
        clearTimeout(commandUplinkTimerRef.current);
        commandUplinkTimerRef.current = null;
      }
      setUplinkEnabledRef.current(
        effectiveVoiceInteractionPolicy(
          snapshot.active_mode,
          interactionPolicyRef.current,
        ) === "continuous",
      );
    }
    return true;
  }, []);

  const refreshModeSnapshot = useCallback(async (): Promise<ModeStateSnapshot | null> => {
    const sessionId = sessionIdRef.current;
    const tickets = realtimeTicketCacheRef.current;
    if (!tickets || !sessionId || !runningRef.current) return null;
    try {
      const snapshot = await withRealtimeTicket(tickets, (ticket) =>
        getRealtimeModeState(ticket, sessionId),
      );
      if (sessionIdRef.current !== sessionId) return null;
      applyModeSnapshot(snapshot);
      return modeStateRef.current;
    } catch {
      // Older realtime deployments do not expose mode yet. Keep interpretation
      // controls hidden and preserve the existing translation path.
      return null;
    }
  }, [applyModeSnapshot]);

  const refreshControlSnapshots = useCallback(async () => {
    const sessionId = sessionIdRef.current;
    const tickets = realtimeTicketCacheRef.current;
    if (!tickets || !sessionId || !runningRef.current) return;

    const modeResult = await withRealtimeTicket(tickets, (ticket) =>
      getRealtimeModeState(ticket, sessionId),
    ).then(
      (value) => ({ status: "fulfilled" as const, value }),
      (reason) => ({ status: "rejected" as const, reason }),
    );
    if (sessionIdRef.current !== sessionId) return;
    if (modeResult.status === "fulfilled") {
      applyModeSnapshot(modeResult.value);
    }
  }, [applyModeSnapshot]);

  const updateConfig = useCallback((next: VoiceSessionConfig) => {
    const normalized = normalizeVoiceConfig(next);
    const configRevision = configRevisionRef.current + 1;
    configRevisionRef.current = configRevision;
    setAutomaticOutputMessage(null);
    configRef.current = normalized;
    setVoiceConfig(normalized);
    saveVoiceConfig(normalized);

    const token = accessTokenRef.current;
    const sessionId = sessionIdRef.current;
    if (!runningRef.current || !token || !sessionId) {
      lastAppliedVoiceConfigRef.current = normalized;
      setConfigSyncStatus("idle");
      return;
    }
    setConfigSyncStatus("saving");
    pendingConfigUpdatesRef.current += 1;

    const update = activeConfigUpdateChainRef.current
      .catch(() => undefined)
      .then(async () => {
        if (!runningRef.current || sessionIdRef.current !== sessionId) return;
        let expectedVersion = activeLanguageConfigVersionRef.current;
        if (expectedVersion === null) {
          const current = await getCurrentLanguageConfig(token, sessionId);
          expectedVersion = current.version;
        }
        const updated = await createLanguageConfig(
          token,
          sessionId,
          normalized,
          expectedVersion,
        );
        activeLanguageConfigVersionRef.current = updated.version;
        lastAppliedVoiceConfigRef.current = normalized;
        if (
          sessionIdRef.current === sessionId &&
          configRevisionRef.current === configRevision
        ) {
          configRef.current = normalized;
          setVoiceConfig(normalized);
          saveVoiceConfig(normalized);
          setConfigSyncStatus("applied");
        }
      })
      .catch((error) => {
        if (
          sessionIdRef.current === sessionId &&
          error instanceof ApiError &&
          error.code === "version_conflict"
        ) {
          activeLanguageConfigVersionRef.current = null;
        }
        if (
          sessionIdRef.current === sessionId &&
          configRevisionRef.current === configRevision
        ) {
          const previous = lastAppliedVoiceConfigRef.current;
          configRef.current = previous;
          setVoiceConfig(previous);
          saveVoiceConfig(previous);
          setConfigSyncStatus("failed");
          setHintMessage(errorMessage(error, "切换输出模式失败"));
        }
      });
    activeConfigUpdateChainRef.current = update;
    void update.finally(() => {
      pendingConfigUpdatesRef.current -= 1;
    });
  }, []);

  const stopPolling = useCallback(() => {
    if (pollTimerRef.current) {
      clearInterval(pollTimerRef.current);
      pollTimerRef.current = null;
    }
  }, []);

  const clearCommandUplinkTimer = useCallback(() => {
    if (!commandUplinkTimerRef.current) return;
    clearTimeout(commandUplinkTimerRef.current);
    commandUplinkTimerRef.current = null;
  }, []);

  const closeCommandUplink = useCallback(() => {
    clearCommandUplinkTimer();
    const mode = modeStateRef.current?.active_mode ?? initialMode;
    if (
      effectiveVoiceInteractionPolicy(mode, interactionPolicyRef.current) ===
      "wake_word"
    ) {
      setUplinkEnabledRef.current(false);
    }
  }, [clearCommandUplinkTimer, initialMode]);

  const armCommandUplinkTimeout = useCallback(
    (commandId: string) => {
      clearCommandUplinkTimer();
      commandUplinkTimerRef.current = setTimeout(() => {
        commandUplinkTimerRef.current = null;
        if (activeCommandIdRef.current !== commandId) return;
        activeCommandIdRef.current = null;
        const mode = modeStateRef.current?.active_mode ?? initialMode;
        if (
          effectiveVoiceInteractionPolicy(mode, interactionPolicyRef.current) ===
          "wake_word"
        ) {
          setUplinkEnabledRef.current(false);
        }
        setCommandFeedback(null);
        setHintMessage("本轮唤醒已超时，麦克风上行已关闭，请再次说「小灵小灵」。");
      }, COMMAND_UPLINK_TIMEOUT_MS);
    },
    [clearCommandUplinkTimer, initialMode],
  );

  const cleanupMedia = useCallback(() => {
    clearCommandUplinkTimer();
    setUplinkEnabledRef.current = () => undefined;
    openCommandUplinkRef.current = () => undefined;
    webrtcRef.current?.close();
    webrtcRef.current = null;
    const activeTurn = activePartialTurnRef.current;
    if (activeTurn) settledPartialTurnsRef.current.add(activeTurn);
    activePartialTurnRef.current = null;
    partialTextByTurnRef.current.clear();
    dispatch({ type: "CLEAR_ASR_PARTIAL" });
  }, [clearCommandUplinkTimer]);

  const setInteractionPolicy = useCallback((policy: VoiceInteractionPolicy) => {
    if (
      modeStateRef.current?.active_mode === "interpretation" &&
      policy !== "continuous"
    ) {
      setHintMessage("同声传译仅支持常驻模式；仍可说「小灵小灵」发送退出指令。");
      return;
    }
    clearCommandUplinkTimer();
    interactionPolicyRef.current = policy;
    setInteractionPolicyState(policy);
    saveVoiceInteractionPolicy(policy);
    if (!runningRef.current) return;

    setUplinkEnabledRef.current(policy === "continuous");
    activeCommandIdRef.current = null;
    setCommandFeedback(null);
    setHintMessage(
      policy === "continuous"
        ? "已切换到常驻模式，可以直接对话。"
        : "已切换到唤醒词模式；只有说「小灵小灵」后才会开放一轮语音。",
    );
  }, [clearCommandUplinkTimer]);

  const syncAutomaticOutputStatus = useCallback(
    async (
      items: AutomaticOutputStatus[],
      token: string,
      sessionId: string,
    ) => {
      const status = items.find((item) => automaticOutputStatusMessage(item.status));
      if (!status) return;

      const message = automaticOutputStatusMessage(status.status);
      if (!message) return;
      const statusKey = `${status.turn_id}:${status.status}:${status.updated_at}`;

      if (
        status.status === "restored" &&
        pendingConfigUpdatesRef.current > 0
      ) {
        setAutomaticOutputMessage(message);
        return;
      }
      if (latestAutomaticOutputStatusRef.current === statusKey) return;
      latestAutomaticOutputStatusRef.current = statusKey;
      setAutomaticOutputMessage(message);

      if (status.status !== "restored") return;
      const configRevision = configRevisionRef.current;
      try {
        const current = await getCurrentLanguageConfig(token, sessionId);
        if (
          sessionIdRef.current !== sessionId ||
          configRevisionRef.current !== configRevision
        ) {
          return;
        }
        const next = voiceConfigFromLanguageConfig(current, configRef.current);
        configRef.current = next;
        lastAppliedVoiceConfigRef.current = next;
        activeLanguageConfigVersionRef.current = current.version;
        setVoiceConfig(next);
        saveVoiceConfig(next);
        setConfigSyncStatus("applied");
      } catch (error) {
        if (sessionIdRef.current === sessionId) {
          latestAutomaticOutputStatusRef.current = null;
          setHintMessage(errorMessage(error, "同步恢复后的语言配置失败"));
        }
      }
    },
    [],
  );

  const syncTurnsAndState = useCallback(async () => {
    const token = accessTokenRef.current;
    const sessionId = sessionIdRef.current;
    if (!token || !sessionId || !runningRef.current) return;

    try {
      const [turnsPage, snapshot, automaticOutput] = await Promise.all([
        listSessionTurns(token, sessionId),
        getVoiceSessionState(token, sessionId),
        listAutomaticOutputStatus(token, sessionId).catch(() => null),
      ]);

      dispatch({
        type: "SET_TURNS",
        turns: turnsPage.items.map(toTranslationTurn),
      });

      const phase = mapRuntimePhase(snapshot.runtime_state);
      if (phase === "processing") dispatch({ type: "PROCESSING" });
      else if (phase === "playing") dispatch({ type: "PLAYING" });
      else dispatch({ type: "ACTIVATE" });

      setStatusMessage(
        mapRuntimeToStatus(
          snapshot.runtime_state,
          modeStateRef.current?.active_mode ?? initialMode,
        ),
      );
      setDebug((prev) => ({
        ...prev,
        runtimeState: snapshot.runtime_state,
        lastError: snapshot.last_error_code,
      }));

      if (automaticOutput) {
        void syncAutomaticOutputStatus(automaticOutput.items, token, sessionId);
      }

      if (snapshot.runtime_state === "failed") {
        setHintMessage(mapRuntimeFailureHint(snapshot.last_error_code));
      } else if (snapshot.last_error_code) {
        setHintMessage(`last_error_code: ${snapshot.last_error_code}`);
      }
      void refreshControlSnapshots();
    } catch (error) {
      setHintMessage(errorMessage(error, "轮询会话状态失败"));
    }
  }, [initialMode, refreshControlSnapshots, syncAutomaticOutputStatus]);

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
    wakeRef.current?.stop();

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
    realtimeTicketCacheRef.current?.clear();
    modeStateRef.current = null;
    modeSnapshotTrackerRef.current.reset();
    modeOperationRef.current = null;
    activeLanguageConfigVersionRef.current = null;
    latestAutomaticOutputStatusRef.current = null;
    settledPartialTurnsRef.current = new Set();
    activePartialTurnRef.current = null;
    partialTextByTurnRef.current.clear();
    activeCommandIdRef.current = null;
    setCommandFeedback(null);
    setAutomaticOutputMessage(null);
    setConfigSyncStatus("idle");
    dispatch({ type: "END" });
    setStatusMessage(initialMode === "assistant" ? "轻触开启助手" : "轻触开启传译");
    setHintMessage(null);
    setDebug((prev) => ({
      accountId: accountIdRef.current,
      sessionId: null,
      runtimeState: null,
      connectionState: null,
      modeState: null,
      modeCommandPending: false,
      lastError: null,
      wakeStatus: prev.wakeStatus,
    }));
  }, [cleanupMedia, initialMode, stopPolling]);

  const switchMode = useCallback(
    async (targetMode: RealtimeMode) => {
      const sessionId = sessionIdRef.current;
      const current = modeStateRef.current;
      const tickets = realtimeTicketCacheRef.current;
      if (!tickets || !sessionId || !current || !runningRef.current) {
        setHintMessage("当前实时模式快照不可用，继续使用传统同传入口。");
        return;
      }

      const operationId = newIdempotencyKey("mode-operation");
      const command = {
        session_id: sessionId,
        runtime_instance_id: current.runtime_instance_id,
        operation_id: operationId,
        trace_id: newIdempotencyKey("mode-trace"),
        expected_generation: current.generation,
        target_mode: targetMode,
      };
      modeOperationRef.current = operationId;
      setDebug((prev) => ({ ...prev, modeCommandPending: true }));
      try {
        const requestId = newIdempotencyKey("mode-request");
        const result = await withRealtimeTicket(tickets, (ticket) =>
          switchRealtimeMode(ticket, sessionId, command, requestId),
        );
        if (sessionIdRef.current !== sessionId) return;
        if (result.state.runtime_instance_id !== command.runtime_instance_id) {
          await refreshModeSnapshot();
          setHintMessage("realtime runtime 已变化，旧模式命令已丢弃，请重新选择模式。");
          return;
        }
        if (!applyModeSnapshot(result.state)) {
          setHintMessage("模式命令响应已过期，已保留较新的模式状态。");
          return;
        }
        setHintMessage(
          result.status === "unchanged"
            ? `已处于${targetMode === "assistant" ? "助手" : "同声传译"}模式。`
            : `已切换到${targetMode === "assistant" ? "助手" : "同声传译"}模式。`,
        );
      } catch (error) {
        const conflict =
          error instanceof ApiError &&
          (error.code === "mode_generation_conflict" ||
            error.code === "mode_runtime_instance_mismatch" ||
            error.code === "mode_operation_conflict");
        if (conflict) {
          // A changed generation/runtime instance invalidates this command.
          // Refresh only; never replay the stale operation automatically.
          const refreshed = await refreshModeSnapshot();
          setHintMessage(
            refreshed
              ? "模式状态已更新，请重新选择目标模式。"
              : "模式状态已变化，请稍后重试。",
          );
        } else {
          setHintMessage(errorMessage(error, "模式切换失败，当前同传仍可继续。"));
        }
      } finally {
        if (
          sessionIdRef.current === sessionId &&
          modeOperationRef.current === operationId
        ) {
          modeOperationRef.current = null;
          setDebug((prev) => ({ ...prev, modeCommandPending: false }));
        }
      }
    },
    [applyModeSnapshot, refreshModeSnapshot],
  );

  const start = useCallback(async () => {
    if (runningRef.current) return;

    runningRef.current = true;
    const startAbort = new AbortController();
    startAbortRef.current = startAbort;
    dispatch({ type: "START" });
    setStatusMessage(initialMode === "assistant" ? "正在启动助手" : "正在启动传译");
    setHintMessage("连接 xe6-tsy API…");
    latestAutomaticOutputStatusRef.current = null;
    settledPartialTurnsRef.current = new Set();
    activePartialTurnRef.current = null;
    activeCommandIdRef.current = null;
    setCommandFeedback(null);
    setAutomaticOutputMessage(null);
    setDebug((prev) => ({
      accountId: null,
      sessionId: null,
      runtimeState: null,
      connectionState: "connecting",
      modeState: null,
      modeCommandPending: false,
      lastError: null,
      wakeStatus: prev.wakeStatus,
    }));

    let startupAccessToken: string | null = null;
    let startupSessionId: string | null = null;
    const startupResources: {
      webrtc: WebRTCSessionHandles | null;
      ownsSession: boolean;
    } = {
      webrtc: null,
      ownsSession: false,
    };
    const ensureStartupActive = () => {
      if (
        startAbort.signal.aborted ||
        startAbortRef.current !== startAbort ||
        !runningRef.current
      ) {
        throw new DOMException("语音会话启动已取消", "AbortError");
      }
    };

    try {
      const wakeStart = wakeRef.current?.start().catch(() => undefined);
      const auth = await getAuthSession();
      ensureStartupActive();
      startupAccessToken = auth.tokens.access_token;
      accessTokenRef.current = auth.tokens.access_token;
      accountIdRef.current = auth.account.id;
      setDebug((prev) => ({ ...prev, accountId: auth.account.id }));
      if (configRef.current.outputMode === "single") {
        const ready = await hasReadyAutomaticTarget(
          auth.tokens.access_token,
        ).catch(() => false);
        ensureStartupActive();
        if (!ready) {
          const fallbackConfig = {
            ...configRef.current,
            outputMode: "bidirectional" as const,
          };
          configRef.current = fallbackConfig;
          configRevisionRef.current += 1;
          setVoiceConfig(fallbackConfig);
          saveVoiceConfig(fallbackConfig);
        }
      }
      setStatusMessage("正在创建会话");

      const session = await createVoiceSession(auth.tokens.access_token);
      startupSessionId = session.id;
      startupResources.ownsSession = true;
      ensureStartupActive();
      sessionIdRef.current = session.id;
      startupResources.ownsSession = false;
      setDebug((prev) => ({ ...prev, sessionId: session.id }));
      setHintMessage(`session: ${session.id}`);

      setStatusMessage("正在配置语言");
      const languageConfig = await createLanguageConfig(
        auth.tokens.access_token,
        session.id,
        configRef.current,
      );
      ensureStartupActive();
      activeLanguageConfigVersionRef.current = languageConfig.version;
      lastAppliedVoiceConfigRef.current = configRef.current;
      setHintMessage(
        `${formatActivePair(configRef.current)} · ${session.id}`,
      );

      setStatusMessage("正在申请实时票据");
      const ticketResponse = await mintRealtimeTicket(
        auth.tokens.access_token,
        session.id,
      );
      ensureStartupActive();
      const ticket = ticketResponse.ticket;
      realtimeTicketCacheRef.current?.seed(ticketResponse);

      let sessionStream: MediaStream | null = null;
      let sessionUsesWakeUplink = false;
      let sessionUplinkEnabled = false;
      let sessionOutputSuppressed = false;
      let ttsResumeTimer: ReturnType<typeof setTimeout> | null = null;
      const applyRawTrackState = () => {
        for (const track of sessionStream?.getAudioTracks() ?? []) {
          track.enabled = sessionUplinkEnabled && !sessionOutputSuppressed;
        }
      };
      const setSessionUplinkEnabled = (enabled: boolean) => {
        sessionUplinkEnabled = enabled;
        if (sessionUsesWakeUplink) {
          const mode = modeStateRef.current?.active_mode ?? initialMode;
          if (!shouldSuppressMicrophoneDuringTTS(mode)) {
            // Interpretation is full duplex: TTS playback must not mute the
            // capture bridge used by both WebRTC uplink and local KWS.
            wakeRef.current?.setOutputSuppressed(false);
          }
          wakeRef.current?.setUplinkEnabled(enabled);
          return;
        }
        applyRawTrackState();
      };
      const setSessionOutputSuppressed = (suppressed: boolean) => {
        sessionOutputSuppressed = suppressed;
        if (sessionUsesWakeUplink) {
          wakeRef.current?.setOutputSuppressed(suppressed);
          return;
        }
        applyRawTrackState();
      };
      const setTTSOutputSuppressed = (suppressed: boolean) => {
        if (sessionIdRef.current !== session.id) return;
        const mode = modeStateRef.current?.active_mode ?? initialMode;
        if (!shouldSuppressMicrophoneDuringTTS(mode)) {
          // Ordinary interpretation speech is never a barge-in signal. Keep
          // AEC, noise suppression, AGC, and the microphone uplink active.
          return;
        }
        const stream = sessionStream;
        if (!stream) return;
        if (ttsResumeTimer) {
          clearTimeout(ttsResumeTimer);
          ttsResumeTimer = null;
        }
        if (suppressed) {
          setSessionOutputSuppressed(true);
          return;
        }
        ttsResumeTimer = setTimeout(() => {
          ttsResumeTimer = null;
          if (sessionIdRef.current !== session.id) return;
          setSessionOutputSuppressed(false);
        }, TTS_INPUT_RESUME_DELAY_MS);
      };
      setUplinkEnabledRef.current = (enabled) => {
        if (sessionIdRef.current !== session.id) return;
        setSessionUplinkEnabled(enabled);
      };

      setStatusMessage("正在建立 WebRTC");
      setHintMessage("复用已授权麦克风，交换 SDP/ICE。");
      await wakeStart;
      ensureStartupActive();
      const wakeTracks = wakeRef.current?.cloneAudioTracksForPeer() ?? [];
      sessionUsesWakeUplink = wakeTracks.length > 0;
      openCommandUplinkRef.current = () => {
        if (sessionIdRef.current !== session.id) return;
        sessionUplinkEnabled = true;
        if (sessionUsesWakeUplink) {
          wakeRef.current?.openCommandUplink();
          return;
        }
        applyRawTrackState();
      };
      try {
        startupResources.webrtc = await openWebRTCSession({
          ticket,
          sessionId: session.id,
          audioTracks: wakeTracks.length > 0 ? wakeTracks : undefined,
          onDataMessage: (payload) => {
            const phraseSubtitle = parsePhraseSubtitle(payload);
            if (phraseSubtitle && phraseSubtitle.sessionId === session.id) {
              if (settledPartialTurnsRef.current.has(phraseSubtitle.utteranceId)) return;
              dispatch({
                type: "ADD_PHRASE_SUBTITLE",
                subtitle: {
                  utteranceId: phraseSubtitle.utteranceId,
                  phraseSequence: phraseSubtitle.phraseSequence,
                  sourceText: phraseSubtitle.sourceText,
                  translatedText: phraseSubtitle.translatedText,
                  status: phraseSubtitle.status,
                },
              });
              return;
            }
            const partial = parseASRPartial(payload);
            if (partial && partial.sessionId === session.id) {
              if (settledPartialTurnsRef.current.has(partial.turnId)) return;
              activePartialTurnRef.current = partial.turnId;
              partialTextByTurnRef.current.set(partial.turnId, partial.text);
              dispatch({
                type: "SET_ASR_PARTIAL",
                partial: {
                  turnId: partial.turnId,
                  text: partial.text,
                  sourceLanguage: partial.sourceLanguage,
                },
              });
              return;
            }
            const commandResult = parseCommandResult(payload);
            if (commandResult && commandResult.session_id === session.id) {
              if (activeCommandIdRef.current !== commandResult.command_id) return;
              setCommandFeedback({
                commandId: commandResult.command_id,
                status: commandResult.status,
                message: commandResult.message,
              });
              activeCommandIdRef.current = null;
              closeCommandUplink();
              if (
                commandResult.status === "applied" ||
                commandResult.status === "unchanged"
              ) {
                void refreshModeSnapshot();
              }
              return;
            }
            const audio = parseTTSAudioEvent(payload);
            if (audio) {
              enqueueTTSAudio(audio, (playing) => {
                setTTSOutputSuppressed(playing);
              });
              return;
            }
            const assistantReply = parseAssistantReply(payload);
            if (assistantReply) {
              const source = assistantReply.turnId
                ? partialTextByTurnRef.current.get(assistantReply.turnId) ?? ""
                : "";
              if (assistantReply.turnId) {
                settledPartialTurnsRef.current.add(assistantReply.turnId);
                partialTextByTurnRef.current.delete(assistantReply.turnId);
                if (activePartialTurnRef.current === assistantReply.turnId) {
                  activePartialTurnRef.current = null;
                  dispatch({ type: "CLEAR_ASR_PARTIAL" });
                }
              }
              dispatch({
                type: "ADD_ASSISTANT_REPLY",
                reply: {
                  replyId: assistantReply.eventId,
                  turnId: assistantReply.turnId,
                  source,
                  text: assistantReply.text,
                  language: assistantReply.language,
                },
              });
              setStatusMessage("助手已回复");
              return;
            }
            const event = parseTranslationFinal(payload);
            if (!event) return;
            settledPartialTurnsRef.current.add(event.turnId);
            partialTextByTurnRef.current.delete(event.turnId);
            if (activePartialTurnRef.current === event.turnId) {
              activePartialTurnRef.current = null;
            }
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
          onConnectionStateChange: (connectionState) => {
            if (sessionIdRef.current !== session.id) return;
            setDebug((prev) => ({ ...prev, connectionState }));
            if (connectionState === "disconnected") {
              const activeTurn = activePartialTurnRef.current;
              if (activeTurn) settledPartialTurnsRef.current.add(activeTurn);
              activePartialTurnRef.current = null;
              dispatch({ type: "CLEAR_ASR_PARTIAL" });
              setHintMessage("实时连接暂时中断，正在等待浏览器恢复媒体连接。");
            } else if (connectionState === "failed" || connectionState === "closed") {
              const activeTurn = activePartialTurnRef.current;
              if (activeTurn) settledPartialTurnsRef.current.add(activeTurn);
              activePartialTurnRef.current = null;
              dispatch({ type: "CLEAR_ASR_PARTIAL" });
              setHintMessage("实时媒体连接已失效，请结束当前会话后重新开始。");
            } else if (connectionState === "connected") {
              void refreshControlSnapshots();
            }
          },
        });
        ensureStartupActive();
        webrtcRef.current = startupResources.webrtc;
        sessionStream = startupResources.webrtc.localStream;
        setUplinkEnabledRef.current(
          effectiveVoiceInteractionPolicy(
            initialMode,
            interactionPolicyRef.current,
          ) === "continuous",
        );
        startupResources.webrtc = null;
      } catch (webrtcError) {
        const detail = errorMessage(webrtcError, "WebRTC 信令失败");
        throw new Error(
          `WebRTC/realtime 信令失败：${detail} ` +
            `API 侧匿名登录/建会话/语言配置/ticket 已成功（session=${session.id}）。` +
            `请确认已用最新代码重启 start-local.bat（:8080 + :8090），` +
            `且 API 与 realtime 的 REALTIME_TICKET_SECRET 一致。`,
        );
      }

      setStatusMessage(initialMode === "assistant" ? "正在启动助手" : "正在启动传译");
      setHintMessage("WebRTC 已 connected，正在调用 API /start…");
      try {
        await startVoiceSession(
          auth.tokens.access_token,
          session.id,
          undefined,
          startAbort.signal,
          initialMode,
        );
        ensureStartupActive();
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
      const wakeHint = activeHintForWake(
        wakeRef.current?.getStatus() ?? "error",
      );
      setHintMessage(
        wakeHint ??
          (effectiveVoiceInteractionPolicy(
            initialMode,
            interactionPolicyRef.current,
          ) === "wake_word"
            ? "唤醒词模式 · 说「小灵小灵」后开放一轮对话"
            : initialMode === "assistant"
              ? "助手已开启 · 可直接提问 · 说「小灵小灵」后可用自然语言切换模式"
              : `传译已开启 · ${formatActivePair(configRef.current)} · 说「小灵小灵」后可用自然语言切换模式`),
      );
      setDebug((prev) => ({ ...prev, connectionState: "connected" }));
      startPolling();
    } catch (error) {
      const startupCancelled =
        startAbort.signal.aborted ||
        startAbortRef.current !== startAbort ||
        !runningRef.current;
      if (startupCancelled) {
        const unclaimedWebRTC = startupResources.webrtc;
        unclaimedWebRTC?.close();
        if (webrtcRef.current === unclaimedWebRTC) {
          webrtcRef.current = null;
        }
        if (sessionIdRef.current === startupSessionId) {
          sessionIdRef.current = null;
        }
        if (
          startupResources.ownsSession &&
          startupAccessToken &&
          startupSessionId
        ) {
          void endVoiceSession(
            startupAccessToken,
            startupSessionId,
            "operator_cancelled",
          ).catch(() => undefined);
        }
        return;
      }
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
      wakeRef.current?.stop();
      stopPolling();
      sessionIdRef.current = null;
      realtimeTicketCacheRef.current?.clear();
      modeStateRef.current = null;
      modeSnapshotTrackerRef.current.reset();
      modeOperationRef.current = null;
      activeLanguageConfigVersionRef.current = null;
      activePartialTurnRef.current = null;
      latestAutomaticOutputStatusRef.current = null;
      activeCommandIdRef.current = null;
      setCommandFeedback(null);
      setAutomaticOutputMessage(null);
      setConfigSyncStatus("idle");
      setDebug((prev) => ({
        ...prev,
        connectionState: null,
        modeState: null,
        modeCommandPending: false,
      }));
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
  }, [
    cleanupMedia,
    closeCommandUplink,
    initialMode,
    refreshControlSnapshots,
    refreshModeSnapshot,
    startPolling,
    stopPolling,
  ]);

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
      onWake: (keyword) => {
        const session = webrtcRef.current;
        if (!runningRef.current || !sessionIdRef.current || !session) return;
        const activeTurn = activePartialTurnRef.current;
        if (activeTurn) {
          settledPartialTurnsRef.current.add(activeTurn);
          activePartialTurnRef.current = null;
          dispatch({ type: "CLEAR_ASR_PARTIAL" });
        }
        const result = sendWakeWordDetectedSignal(session);
        if (result.ok) {
          const mode = modeStateRef.current?.active_mode ?? initialMode;
          if (
            effectiveVoiceInteractionPolicy(mode, interactionPolicyRef.current) ===
            "wake_word"
          ) {
            openCommandUplinkRef.current();
            armCommandUplinkTimeout(result.signal.signal_id);
          }
          activeCommandIdRef.current = result.signal.signal_id;
          setCommandFeedback({
            commandId: result.signal.signal_id,
            status: "listening",
            message: `已识别「${keyword}」，正在听取指令`,
          });
          return;
        }
        setHintMessage(
          result.reason === "data_channel_not_open"
            ? "已识别唤醒词，但实时控制通道尚未就绪，请重试。"
            : "已识别唤醒词，但发送失败，请重试。",
        );
      },
      onStatus: (status, detail) => {
        setWakeStatus(status);
        setDebug((prev) => ({ ...prev, wakeStatus: status }));
        if (runningRef.current) {
          const activeHint = activeHintForWake(status);
          if (activeHint) {
            setHintMessage(detail ? `${activeHint} ${detail}` : activeHint);
          } else if (status === "listening") {
            setHintMessage("说「小灵小灵」后，可用自然语言切换模式。");
          }
          return;
        }
        if (status === "listening") {
          setStatusMessage(initialMode === "assistant" ? "轻触开启助手" : "轻触开启传译");
          setHintMessage(idleHintForWake(status, initialMode));
          return;
        }
        if (status === "error") {
          setStatusMessage("轻触开始");
          setHintMessage(detail ?? idleHintForWake(status, initialMode));
          return;
        }
        if (status === "requesting_mic" || status === "loading_model") {
          setStatusMessage(
            status === "requesting_mic" ? "请允许麦克风" : "正在加载唤醒模型",
          );
          setHintMessage(detail ?? idleHintForWake(status, initialMode));
        }
      },
    });
    wakeRef.current = listener;

    return () => {
      wakeRef.current = null;
      listener.stop();
    };
  }, [armCommandUplinkTimeout, initialMode]);

  useEffect(
    () => () => {
      runningRef.current = false;
      startAbortRef.current?.abort();
      startAbortRef.current = null;
      stopPolling();
      cleanupMedia();
      wakeRef.current?.stop();
    },
    [cleanupMedia, stopPolling],
  );

  return useMemo(
    () => ({
      state,
      transientASRSubtitle: state.asrPartial,
      transientPhraseSubtitles: state.phraseSubtitles,
      latestTurn: state.turns.at(-1),
      latestAssistantReply: state.assistantReplies.at(-1),
      activeMode,
      statusMessage,
      hintMessage: hintMessage ?? state.notice,
      automaticOutputMessage,
      voiceConfig,
      configSyncStatus,
      updateConfig,
      debug,
      wakeStatus,
      commandFeedback,
      interactionPolicy: effectiveInteractionPolicy,
      interactionPolicyLocked: activeMode === "interpretation",
      setInteractionPolicy,
      switchMode,
      toggle,
    }),
    [
      debug,
      activeMode,
      automaticOutputMessage,
      configSyncStatus,
      commandFeedback,
      effectiveInteractionPolicy,
      hintMessage,
      state,
      statusMessage,
      switchMode,
      setInteractionPolicy,
      toggle,
      updateConfig,
      voiceConfig,
      wakeStatus,
    ],
  );
}
