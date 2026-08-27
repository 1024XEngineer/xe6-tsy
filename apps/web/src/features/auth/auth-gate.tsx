"use client";

import { ArrowLeft, ArrowRight } from "@phosphor-icons/react";
import { FormEvent, useEffect, useState } from "react";

import { VoiceExperience } from "@/features/voice/components/voice-experience";
import {
  clearAuthSession,
  getAuthSession,
  loadAuthSession,
  saveAuthSession,
} from "@/features/voice/lib/auth-session";
import { ApiError } from "@/features/voice/lib/http";
import {
  loginWithPhone,
  logoutAccount,
  requestPhoneVerificationCode,
} from "@/features/voice/lib/lingow-api";

import styles from "./auth.module.css";
import Ferrofluid from "./ferrofluid";

const RESEND_SECONDS = 60;

type AuthView = "checking" | "login" | "authenticated";

function errorMessage(error: unknown, fallback: string): string {
  if (!(error instanceof ApiError)) return fallback;
  if (error.status === 429) return "请求过于频繁，请稍后再试";
  if (error.code === "invalid_verification_code") return "验证码不正确，请重新输入";
  if (error.code === "verification_challenge_expired") return "验证码已过期，请重新获取";
  return error.message.replace(/^\[[^\]]+\]\s*/, "") || fallback;
}

function normalizeMainlandPhone(value: string): string | null {
  const digits = value.replace(/\D/g, "");
  return /^1\d{10}$/.test(digits) ? `+86${digits}` : null;
}

export function AuthGate() {
  const [view, setView] = useState<AuthView>("checking");

  useEffect(() => {
    let active = true;
    void getAuthSession().then(
      () => {
        if (active) setView("authenticated");
      },
      () => {
        if (active) setView("login");
      },
    );
    return () => {
      active = false;
    };
  }, []);

  const handleLogout = async () => {
    const refreshToken = loadAuthSession()?.tokens.refresh_token;
    clearAuthSession();
    setView("login");
    if (refreshToken) {
      try {
        await logoutAccount(refreshToken);
      } catch {
        // Local sign-out must succeed even when the API is unavailable.
      }
    }
  };

  if (view === "authenticated") {
    return <VoiceExperience onLogout={handleLogout} />;
  }

  if (view === "checking") {
    return (
      <main aria-label="正在检查登录状态" className={styles.loading}>
        <span className={styles.loadingMark}>lingow</span>
      </main>
    );
  }

  return <PhoneLogin onAuthenticated={() => setView("authenticated")} />;
}

type PhoneLoginProps = {
  onAuthenticated: () => void;
};

export function PhoneLogin({ onAuthenticated }: PhoneLoginProps) {
  const [phone, setPhone] = useState("");
  const [challengeId, setChallengeId] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [cooldown, setCooldown] = useState(0);

  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = window.setTimeout(() => setCooldown(cooldown - 1), 1_000);
    return () => window.clearTimeout(timer);
  }, [cooldown]);

  const requestCode = async () => {
    const normalizedPhone = normalizeMainlandPhone(phone);
    if (!normalizedPhone) {
      setError("请输入正确的 11 位手机号码");
      return;
    }

    setPending(true);
    setError("");
    try {
      const challenge = await requestPhoneVerificationCode(normalizedPhone);
      setChallengeId(challenge.challenge_id);
      setCooldown(RESEND_SECONDS);
    } catch (requestError) {
      setError(errorMessage(requestError, "验证码发送失败，请稍后重试"));
    } finally {
      setPending(false);
    }
  };

  const submitPhone = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void requestCode();
  };

  const submitCode = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!challengeId) return;
    if (!/^\d{4,6}$/.test(code)) {
      setError("请输入 4 至 6 位验证码");
      return;
    }

    setPending(true);
    setError("");
    try {
      const auth = await loginWithPhone(challengeId, code);
      saveAuthSession(auth);
      onAuthenticated();
    } catch (loginError) {
      setError(errorMessage(loginError, "登录失败，请稍后重试"));
    } finally {
      setPending(false);
    }
  };

  const returnToPhone = () => {
    setChallengeId(null);
    setCode("");
    setError("");
    setCooldown(0);
  };

  return (
    <main className={styles.login}>
      <div className={styles.backgroundLayer}>
        <Ferrofluid
          colors={["#ffffff", "#ffffff", "#ffffff"]}
          flowDirection="down"
          fluidity={0.1}
          glow={2}
          mouseInteraction
          mouseDampening={0.15}
          mouseRadius={0.35}
          mouseStrength={1}
          opacity={1}
          rimWidth={0.2}
          scale={1.6}
          sharpness={2.5}
          shimmer={1.5}
          speed={0.5}
          turbulence={1}
        />
      </div>
      <header className={styles.header}>
        <h1 translate="no">lingow</h1>
        <span>账户登录</span>
      </header>

      <section className={styles.loginContent}>
        <div className={styles.formHeading}>
          <span>{challengeId ? "02" : "01"} / 02</span>
          <h2>{challengeId ? "输入验证码" : "使用手机号登录"}</h2>
          <p>
            {challengeId
              ? `验证码已发送至 +86 ${phone}`
              : "登录后继续使用 Lingow 语音服务"}
          </p>
        </div>

        {challengeId ? (
          <form className={styles.form} onSubmit={submitCode}>
            <label htmlFor="verification-code">验证码</label>
            <input
              autoComplete="one-time-code"
              autoFocus
              id="verification-code"
              inputMode="numeric"
              maxLength={6}
              onChange={(event) => setCode(event.target.value.replace(/\D/g, ""))}
              placeholder="请输入验证码"
              value={code}
            />
            <div aria-live="polite" className={styles.formMessage}>
              {error || "\u00a0"}
            </div>
            <button className={styles.primaryButton} disabled={pending} type="submit">
              <span>{pending ? "登录中" : "登录"}</span>
              <ArrowRight aria-hidden="true" size={19} />
            </button>
            <div className={styles.secondaryActions}>
              <button onClick={returnToPhone} type="button">
                <ArrowLeft aria-hidden="true" size={16} />
                修改手机号
              </button>
              <button
                disabled={pending || cooldown > 0}
                onClick={() => void requestCode()}
                type="button"
              >
                {cooldown > 0 ? `${cooldown} 秒后重发` : "重新发送"}
              </button>
            </div>
          </form>
        ) : (
          <form className={styles.form} onSubmit={submitPhone}>
            <label htmlFor="phone">手机号码</label>
            <div className={styles.phoneField}>
              <span>+86</span>
              <input
                autoComplete="tel-national"
                autoFocus
                id="phone"
                inputMode="tel"
                maxLength={11}
                onChange={(event) => setPhone(event.target.value.replace(/\D/g, ""))}
                placeholder="请输入手机号码"
                value={phone}
              />
            </div>
            <div aria-live="polite" className={styles.formMessage}>
              {error || "\u00a0"}
            </div>
            <button className={styles.primaryButton} disabled={pending} type="submit">
              <span>{pending ? "发送中" : "获取验证码"}</span>
              <ArrowRight aria-hidden="true" size={19} />
            </button>
          </form>
        )}
      </section>

      <footer className={styles.footer}>安全登录 · 会话记录与用量将绑定到当前账户</footer>
    </main>
  );
}
