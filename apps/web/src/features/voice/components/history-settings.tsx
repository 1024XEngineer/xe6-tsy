"use client";

import {
  ArrowLeft,
  CaretRight,
  ClockCounterClockwise,
  X,
} from "@phosphor-icons/react";
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

export function HistoryPreview({
  onOpen,
}: {
  onOpen: (session: VoiceSession) => void;
}) {
  const [sessions, setSessions] = useState<VoiceSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const auth = await getOrCreateAuthSession();
        const page = await listVoiceSessions(auth.tokens.access_token, { limit: 5 });
        if (!cancelled) setSessions(page.sessions);
      } catch (loadError) {
        if (!cancelled) {
          setError(loadError instanceof Error ? loadError.message : "无法加载最近会话");
        }
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className={styles.historySessionsView}>
      <p className={styles.settingsState}>最近 5 次会话</p>
      {loading ? <p className={styles.settingsState}>正在读取会话...</p> : null}
      {error ? <p className={styles.settingsState}>{error}</p> : null}
      {!loading && !error && sessions.length === 0 ? (
        <p className={styles.settingsState}>还没有历史会话</p>
      ) : null}
      <div className={styles.historySessionList}>
        {sessions.map((session) => (
          <button
            className={styles.historySessionRow}
            key={session.id}
            onClick={() => onOpen(session)}
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

export function HistorySettings({
  onExit = () => undefined,
  initialSessionId,
}: {
  onExit?: () => void;
  initialSessionId?: string | null;
}) {
  const [sessions, setSessions] = useState<VoiceSession[]>([]);
  const [selected, setSelected] = useState<VoiceSession | null>(null);
  const [turns, setTurns] = useState<VoiceTurn[]>([]);
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadSessions = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      const loaded: VoiceSession[] = [];
      let cursor: string | undefined;
      do {
        const page = await listVoiceSessions(auth.tokens.access_token, {
          limit: 20,
          cursor,
        });
        loaded.push(...page.sessions);
        cursor = page.next_cursor ?? undefined;
      } while (cursor);
      setSessions(loaded);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "无法加载历史会话");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const requestId = window.setTimeout(() => void loadSessions(), 0);
    return () => window.clearTimeout(requestId);
  }, [loadSessions]);

  const openSession = async (session: VoiceSession) => {
    setSelected(session);
    setTurns([]);
    setDetailLoading(true);
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      const loaded: VoiceTurn[] = [];
      let cursor: string | undefined;
      do {
        const page = await listSessionTurns(
          auth.tokens.access_token,
          session.id,
          100,
          cursor,
        );
        loaded.push(...page.items);
        cursor = page.next_cursor ?? undefined;
      } while (cursor);
      setTurns(loaded);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "无法加载会话记录");
    } finally {
      setDetailLoading(false);
    }
  };

  useEffect(() => {
    if (!initialSessionId || selected || loading) return;
    const session = sessions.find((item) => item.id === initialSessionId);
    if (session) void openSession(session);
  }, [initialSessionId, loading, selected, sessions]);

  return (
    <div className={styles.historyWorkspace}>
      <aside className={styles.historyWorkspaceNav}>
        <div className={styles.historyWorkspaceNavHeader}>
          <div>
            <span className={styles.historyWorkspaceKicker}>ARCHIVE</span>
            <h3>历史会话</h3>
          </div>
          <button aria-label="返回设置" className={styles.iconButton} onClick={onExit} type="button">
            <X aria-hidden="true" size={18} />
          </button>
        </div>
        <p className={styles.historyWorkspaceHint}>选择一次会话，查看完整双语记录。</p>
        {loading ? <p className={styles.settingsState}>正在读取会话...</p> : null}
        {error && !selected ? (
          <div className={styles.settingsState}>
            <p>{error}</p>
            <button onClick={() => void loadSessions()} type="button">重新加载</button>
          </div>
        ) : null}
        {!loading && !error && sessions.length === 0 ? (
          <p className={styles.settingsState}>还没有历史会话</p>
        ) : null}
        <div className={styles.historyWorkspaceSessionList}>
          {sessions.map((session) => (
            <button
              aria-label={`查看${sessionDate(session)}的历史记录`}
              className={`${styles.historyWorkspaceSession} ${selected?.id === session.id ? styles.historyWorkspaceSessionActive : ""}`}
              key={session.id}
              onClick={() => void openSession(session)}
              type="button"
            >
              <ClockCounterClockwise aria-hidden="true" size={17} />
              <span>
                <strong>{sessionDate(session)}</strong>
                <small>{sessionDuration(session)} · {statusLabel(session.status)}</small>
                <small className={styles.historyWorkspaceSessionId}>{session.id.slice(0, 12)}</small>
              </span>
              <CaretRight aria-hidden="true" size={15} />
            </button>
          ))}
        </div>
      </aside>

      <section aria-live="polite" className={styles.historyWorkspaceDetail}>
        {selected ? (
          <>
            <header className={styles.historyWorkspaceDetailHeader}>
              <div>
                <span className={styles.historyWorkspaceKicker}>会话 {selected.id.slice(0, 12)}</span>
                <h3>{sessionDate(selected)} 的记录</h3>
                <p>{sessionDuration(selected)} · {statusLabel(selected.status)}</p>
              </div>
              <button aria-label="返回历史会话" className={styles.historyWorkspaceBack} onClick={() => setSelected(null)} type="button">
                <ArrowLeft aria-hidden="true" size={17} />
                会话列表
              </button>
            </header>
            {detailLoading ? <p className={styles.settingsState}>正在读取双语记录...</p> : null}
            {error ? <p className={styles.settingsState}>{error}</p> : null}
            {!detailLoading && !error && turns.length === 0 ? (
              <p className={styles.settingsState}>这次会话没有翻译记录</p>
            ) : null}
            <div className={styles.historyWorkspaceTranscript}>
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
          </>
        ) : (
          <div className={styles.historyWorkspaceEmpty}>
            <ClockCounterClockwise aria-hidden="true" size={30} />
            <p>选择左侧会话开始查看</p>
          </div>
        )}
      </section>
    </div>
  );
}
