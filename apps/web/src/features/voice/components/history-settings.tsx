"use client";

import { ArrowLeft, CaretRight, ClockCounterClockwise } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";

import { getOrCreateAuthSession } from "../lib/auth-session";
import {
  listSessionTurns,
  listVoiceSessions,
  type VoiceSession,
  type VoiceTurn,
} from "../lib/lingow-api";
import { languageLabel } from "../lib/languages";
import styles from "../voice.module.css";

const dateTimeFormat = new Intl.DateTimeFormat("zh-CN", {
  month: "long",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
});

function sessionDate(session: VoiceSession): string {
  return dateTimeFormat.format(new Date(session.started_at ?? session.created_at));
}

function sessionDuration(session: VoiceSession): string {
  if (!session.started_at || !session.ended_at) return "时长未记录";
  const durationMs = Date.parse(session.ended_at) - Date.parse(session.started_at);
  return `${Math.max(1, Math.round(durationMs / 60_000))} 分钟`;
}

function statusLabel(status: VoiceSession["status"]): string {
  switch (status) {
    case "active":
      return "进行中";
    case "failed":
      return "异常结束";
    case "created":
      return "未开始";
    default:
      return "已结束";
  }
}

export function HistorySettings() {
  const [sessions, setSessions] = useState<VoiceSession[]>([]);
  const [selected, setSelected] = useState<VoiceSession | null>(null);
  const [turns, setTurns] = useState<VoiceTurn[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadSessions = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      const page = await listVoiceSessions(auth.tokens.access_token, { limit: 20 });
      setSessions(page.sessions);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "无法加载历史会话");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const requestId = window.setTimeout(() => {
      void loadSessions();
    }, 0);
    return () => window.clearTimeout(requestId);
  }, [loadSessions]);

  const openSession = async (session: VoiceSession) => {
    setSelected(session);
    setTurns([]);
    setLoading(true);
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      const page = await listSessionTurns(auth.tokens.access_token, session.id, 100);
      setTurns(page.items);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "无法加载会话记录");
    } finally {
      setLoading(false);
    }
  };

  if (selected) {
    return (
      <div className={styles.historyDetailView}>
        <button
          aria-label="返回历史会话"
          className={styles.historyBack}
          onClick={() => setSelected(null)}
          type="button"
        >
          <ArrowLeft aria-hidden="true" size={17} />
          历史会话
        </button>
        <div className={styles.historyIdentity}>
          <strong>{sessionDate(selected)}的记录</strong>
          <span>会话 {selected.id.slice(0, 8)} · {sessionDuration(selected)}</span>
        </div>
        {loading ? <p className={styles.settingsState}>正在读取记录...</p> : null}
        {error ? <p className={styles.settingsState}>{error}</p> : null}
        {!loading && !error && turns.length === 0 ? (
          <p className={styles.settingsState}>这次会话没有翻译记录</p>
        ) : null}
        <div className={styles.historyTranscript}>
          {turns.map((turn) => (
            <article className={styles.historyTranscriptTurn} key={turn.id}>
              <span>{languageLabel(turn.source_language)}</span>
              <div>
                <p>{turn.source_text}</p>
                <p>{turn.translated_text}</p>
              </div>
            </article>
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className={styles.historySessionsView}>
      {loading ? <p className={styles.settingsState}>正在读取历史会话...</p> : null}
      {error ? (
        <div className={styles.settingsState}>
          <p>{error}</p>
          <button onClick={() => void loadSessions()} type="button">重新加载</button>
        </div>
      ) : null}
      {!loading && !error && sessions.length === 0 ? (
        <p className={styles.settingsState}>还没有历史会话</p>
      ) : null}
      <div className={styles.historySessionList}>
        {sessions.map((session) => (
          <button
            aria-label={`查看${sessionDate(session)}的历史记录`}
            className={styles.historySessionRow}
            key={session.id}
            onClick={() => void openSession(session)}
            type="button"
          >
            <ClockCounterClockwise aria-hidden="true" size={18} />
            <span>
              <strong>{sessionDate(session)}</strong>
              <small>{sessionDuration(session)} · {statusLabel(session.status)}</small>
            </span>
            <CaretRight aria-hidden="true" size={16} />
          </button>
        ))}
      </div>
    </div>
  );
}
