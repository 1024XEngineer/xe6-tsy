"use client";

import { SignOut } from "@phosphor-icons/react";
import { useEffect, useState } from "react";

import { getAuthSession, loadAuthSession } from "../lib/auth-session";
import type { AuthResult } from "../lib/lingow-api";
import styles from "../voice.module.css";

function formatAccountDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "—";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "numeric",
    day: "numeric",
  }).format(date);
}

export function AccountSettings({
  onLogout,
  logoutDisabled = false,
}: {
  onLogout?: () => void | Promise<void>;
  logoutDisabled?: boolean;
}) {
  const [auth, setAuth] = useState<AuthResult | null>(() => loadAuthSession());

  useEffect(() => {
    let active = true;
    void getAuthSession().then(
      (nextAuth) => {
        if (active) setAuth(nextAuth);
      },
      () => {
        if (active) setAuth(null);
      },
    );
    return () => {
      active = false;
    };
  }, []);

  return (
    <div className={styles.accountView}>
      <div className={styles.accountStatusRow}>
        <span className={styles.accountStatusDot} />
        <div>
          <strong>已登录</strong>
          <span>手机号验证账户</span>
        </div>
      </div>
      <dl className={styles.accountDetails}>
        <div>
          <dt>账户 ID</dt>
          <dd>{auth?.account.id ?? "—"}</dd>
        </div>
        <div>
          <dt>注册时间</dt>
          <dd>{auth ? formatAccountDate(auth.account.created_at) : "—"}</dd>
        </div>
      </dl>
      {onLogout ? (
        <button
          className={`${styles.textAction} ${styles.dangerAction}`}
          disabled={logoutDisabled}
          onClick={() => void onLogout()}
          title={logoutDisabled ? "请先结束当前对话" : "退出登录"}
          type="button"
        >
          <span>退出登录</span>
          <SignOut aria-hidden="true" size={18} />
        </button>
      ) : null}
      {logoutDisabled ? (
        <p className={styles.settingsState}>请先结束当前对话，再退出登录。</p>
      ) : null}
    </div>
  );
}
