"use client";

import { useCallback, useEffect, useState } from "react";

import { getAuthSession } from "../lib/auth-session";
import { ApiError } from "../lib/http";
import { getAccountUsageSummary } from "../lib/lingow-api";
import styles from "../voice.module.css";

// Replace this display value once the account quota API is available.
const TOTAL_MINUTES = 500;

function currentMonthPeriod(now = new Date()): { start: string; end: string } {
  const start = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1));
  const end = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1));
  return { start: start.toISOString(), end: end.toISOString() };
}

function asrMinutes(audioDurationMs: number): number {
  return Math.round((Math.max(0, audioDurationMs) / 60_000) * 100) / 100;
}

export function UsageSettings() {
  const [usedMinutes, setUsedMinutes] = useState<number | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadUsage = useCallback(async () => {
    setUsedMinutes(null);
    setNotice(null);
    setError(null);
    try {
      const auth = await getAuthSession();
      const period = currentMonthPeriod();
      const summary = await getAccountUsageSummary(
        auth.tokens.access_token,
        period.start,
        period.end,
      );
      const asrTotal = summary.totals
        .filter((total) => total.service_type === "asr")
        .reduce((total, stage) => total + stage.audio_duration_ms, 0);
      setUsedMinutes(asrMinutes(asrTotal));
    } catch (loadError) {
      if (loadError instanceof ApiError && loadError.status === 501) {
        setUsedMinutes(0);
        setNotice("当前后端还没有可用量记录");
        return;
      }
      setError(loadError instanceof Error ? loadError.message : "无法加载用量");
    }
  }, []);

  useEffect(() => {
    const requestId = window.setTimeout(() => {
      void loadUsage();
    }, 0);
    return () => window.clearTimeout(requestId);
  }, [loadUsage]);

  if (error) {
    return (
      <div className={styles.settingsState}>
        <p>{error}</p>
        <button onClick={() => void loadUsage()} type="button">
          重新加载
        </button>
      </div>
    );
  }

  if (usedMinutes === null) {
    return <p className={styles.settingsState}>正在读取本月用量...</p>;
  }

  return (
    <div className={styles.historySetting}>
      <div className={styles.usageSummary}>
        <div className={styles.usageStat}>
          <strong>{TOTAL_MINUTES}</strong>
          <span>本月总额（分钟）</span>
        </div>
        <div className={styles.usageStat}>
          <strong>{usedMinutes}</strong>
          <span>已使用（分钟）</span>
        </div>
      </div>
      {notice ? <p className={styles.settingsState}>{notice}</p> : null}
    </div>
  );
}
