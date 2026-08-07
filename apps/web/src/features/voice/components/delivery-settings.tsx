"use client";

import {
  ArrowClockwise,
  Buildings,
  Check,
  EnvelopeSimple,
  PaperPlaneTilt,
  Trash,
} from "@phosphor-icons/react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { getOrCreateAuthSession } from "../lib/auth-session";
import { ApiError } from "../lib/http";
import {
  bindEmailTarget,
  bindWeChatTarget,
  listMessagePreferences,
  listMessageTargets,
  listOutboundMessages,
  putMessagePreference,
  requestEmailBindVerification,
  revokeMessageTarget,
  type DeliveryChannel,
  type MessagePreference,
  type MessageTarget,
  type OutboundMessage,
} from "../lib/lingow-api";
import styles from "../voice.module.css";

const CHANNELS: readonly DeliveryChannel[] = ["email", "wechat"];

const DELIVERY_STATUS_CLASSES: Record<OutboundMessage["status"], string> = {
  queued: styles.deliveryStatusQueued,
  sending: styles.deliveryStatusSending,
  sent: styles.deliveryStatusSent,
  failed: styles.deliveryStatusFailed,
  retrying: styles.deliveryStatusRetrying,
  cancelled: styles.deliveryStatusCancelled,
};

function channelLabel(channel: DeliveryChannel): string {
  return channel === "email" ? "邮箱" : "企业微信";
}

function statusLabel(status: OutboundMessage["status"]): string {
  return {
    queued: "排队中",
    sending: "发送中",
    sent: "已发送",
    failed: "失败",
    retrying: "重试中",
    cancelled: "已取消",
  }[status];
}

function formatMessageTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "—";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function requestError(error: unknown): string {
  if (error instanceof ApiError && error.status === 501) {
    return "当前环境还没有启用投递服务";
  }
  return error instanceof Error ? error.message : "投递设置暂时不可用";
}

export function DeliverySettings() {
  const [targets, setTargets] = useState<MessageTarget[]>([]);
  const [preferences, setPreferences] = useState<MessagePreference[]>([]);
  const [messages, setMessages] = useState<OutboundMessage[]>([]);
  const [email, setEmail] = useState("");
  const [emailToken, setEmailToken] = useState("");
  const [emailVerificationSent, setEmailVerificationSent] = useState(false);
  const [wechatCode, setWechatCode] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const mountedRef = useRef(true);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      const [targetResult, preferenceResult, messageResult] = await Promise.all([
        listMessageTargets(auth.tokens.access_token),
        listMessagePreferences(auth.tokens.access_token),
        listOutboundMessages(auth.tokens.access_token),
      ]);
      if (!mountedRef.current) return;
      setTargets(targetResult.items);
      setPreferences(preferenceResult.items);
      setMessages(messageResult.items);
    } catch (loadError) {
      if (mountedRef.current) setError(requestError(loadError));
    } finally {
      if (mountedRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const requestId = window.setTimeout(() => {
      void load();
    }, 0);
    return () => {
      mountedRef.current = false;
      window.clearTimeout(requestId);
    };
  }, [load]);

  const activeTargets = useMemo(
    () => targets.filter((target) => target.verified && !target.revoked_at),
    [targets],
  );

  const preferenceFor = (channel: DeliveryChannel) =>
    preferences.find((preference) => preference.channel === channel);

  const targetsFor = (channel: DeliveryChannel) =>
    activeTargets.filter((target) => target.channel === channel);

  const updatePreference = async (
    channel: DeliveryChannel,
    enabled: boolean,
    destinationRef: string,
  ) => {
    if (enabled && !destinationRef) return;
    setBusy(`preference:${channel}`);
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      const updated = await putMessagePreference(
        auth.tokens.access_token,
        channel,
        enabled,
        destinationRef || undefined,
      );
      setPreferences((current) => [
        ...current.filter((preference) => preference.channel !== channel),
        updated,
      ]);
    } catch (updateError) {
      setError(requestError(updateError));
    } finally {
      setBusy(null);
    }
  };

  const requestEmailCode = async () => {
    if (!email.trim()) return;
    setBusy("email:request");
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      await requestEmailBindVerification(auth.tokens.access_token, email.trim());
      setEmailVerificationSent(true);
    } catch (bindError) {
      setError(requestError(bindError));
    } finally {
      setBusy(null);
    }
  };

  const bindEmail = async () => {
    if (!emailToken.trim()) return;
    setBusy("email:bind");
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      await bindEmailTarget(auth.tokens.access_token, emailToken.trim());
      setEmail("");
      setEmailToken("");
      setEmailVerificationSent(false);
      await load();
    } catch (bindError) {
      setError(requestError(bindError));
    } finally {
      setBusy(null);
    }
  };

  const bindWeChat = async () => {
    if (!wechatCode.trim()) return;
    setBusy("wechat:bind");
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      await bindWeChatTarget(auth.tokens.access_token, wechatCode.trim());
      setWechatCode("");
      await load();
    } catch (bindError) {
      setError(requestError(bindError));
    } finally {
      setBusy(null);
    }
  };

  const revoke = async (target: MessageTarget) => {
    setBusy(`revoke:${target.channel}:${target.destination_ref}`);
    setError(null);
    try {
      const auth = await getOrCreateAuthSession();
      await revokeMessageTarget(
        auth.tokens.access_token,
        target.channel,
        target.destination_ref,
      );
      await load();
    } catch (revokeError) {
      setError(requestError(revokeError));
    } finally {
      setBusy(null);
    }
  };

  if (loading) return <p className={styles.settingsState}>正在读取投递设置...</p>;

  return (
    <div className={styles.deliverySettings}>
      <div className={styles.deliveryIntro}>
        <span>单向输出的反向译文会发送到这里选定的一个目标。</span>
        <button
          aria-label="刷新投递设置"
          className={styles.deliveryRefresh}
          disabled={busy !== null}
          onClick={() => void load()}
          type="button"
        >
          <ArrowClockwise aria-hidden="true" size={15} />
        </button>
      </div>

      {error ? <p className={styles.deliveryError} role="alert">{error}</p> : null}

      {CHANNELS.map((channel) => {
        const channelTargets = targetsFor(channel);
        const preference = preferenceFor(channel);
        const selectedRef = channelTargets.some(
          (target) => target.destination_ref === preference?.destination_ref,
        )
          ? preference?.destination_ref ?? ""
          : channelTargets[0]?.destination_ref ?? "";
        const Icon = channel === "email" ? EnvelopeSimple : Buildings;
        return (
          <section className={styles.deliverySection} key={channel}>
            <div className={styles.deliverySectionHeader}>
              <div>
                <h3><Icon aria-hidden="true" size={17} />{channelLabel(channel)}</h3>
                <p>{channelTargets.length ? `${channelTargets.length} 个已验证目标` : "还没有已验证目标"}</p>
              </div>
              {channelTargets.length ? (
                <button
                  aria-label={`${preference?.enabled ? "关闭" : "开启"}${channelLabel(channel)}自动发送`}
                  aria-pressed={preference?.enabled ?? false}
                  className={`${styles.settingToggle} ${preference?.enabled ? styles.settingToggleActive : ""}`}
                  disabled={busy === `preference:${channel}`}
                  onClick={() => void updatePreference(channel, !(preference?.enabled ?? false), selectedRef)}
                  type="button"
                >
                  <span />
                </button>
              ) : null}
            </div>

            {channelTargets.length ? (
              <div className={styles.deliveryTargetList}>
                {channelTargets.map((target) => (
                  <div className={styles.deliveryTargetRow} key={target.destination_ref}>
                    <label>
                      <input
                        checked={selectedRef === target.destination_ref}
                        name={`delivery-${channel}`}
                        onChange={() => void updatePreference(channel, preference?.enabled ?? false, target.destination_ref)}
                        type="radio"
                      />
                      <span>{target.destination_ref}</span>
                    </label>
                    <button
                      aria-label={`撤销${target.destination_ref}`}
                      className={styles.deliveryRevoke}
                      disabled={busy !== null}
                      onClick={() => void revoke(target)}
                      type="button"
                    >
                      <Trash aria-hidden="true" size={15} />
                    </button>
                  </div>
                ))}
              </div>
            ) : null}

            {channel === "email" ? (
              <div className={styles.deliveryForm}>
                <label>
                  邮箱地址
                  <input
                    autoComplete="email"
                    onChange={(event) => setEmail(event.target.value)}
                    placeholder="name@example.com"
                    type="email"
                    value={email}
                  />
                </label>
                {emailVerificationSent ? (
                  <label>
                    验证码
                    <input
                      inputMode="numeric"
                      onChange={(event) => setEmailToken(event.target.value)}
                      placeholder="输入邮件中的 token"
                      value={emailToken}
                    />
                  </label>
                ) : null}
                <button
                  className={styles.deliveryAction}
                  disabled={busy !== null || (emailVerificationSent ? !emailToken.trim() : !email.trim())}
                  onClick={() => void (emailVerificationSent ? bindEmail() : requestEmailCode())}
                  type="button"
                >
                  <EnvelopeSimple aria-hidden="true" size={15} />
                  {emailVerificationSent ? "绑定邮箱" : "发送验证码"}
                </button>
              </div>
            ) : (
              <div className={styles.deliveryForm}>
                <label>
                  企业微信 OAuth code
                  <input
                    onChange={(event) => setWechatCode(event.target.value)}
                    placeholder="粘贴登录回调 code"
                    value={wechatCode}
                  />
                </label>
                <button
                  className={styles.deliveryAction}
                  disabled={busy !== null || !wechatCode.trim()}
                  onClick={() => void bindWeChat()}
                  type="button"
                >
                  <Buildings aria-hidden="true" size={15} />
                  绑定企业微信
                </button>
              </div>
            )}
          </section>
        );
      })}

      <section className={styles.deliverySection}>
        <div className={styles.deliverySectionHeader}>
          <div>
            <h3><PaperPlaneTilt aria-hidden="true" size={17} />最近投递</h3>
            <p>自动发送与手动发送共用这份记录</p>
          </div>
        </div>
        {messages.length ? (
          <div className={styles.deliveryMessageList}>
            {messages.map((message) => (
              <div className={styles.deliveryMessageRow} key={message.id}>
                <div>
                  <strong>{channelLabel(message.channel)} · {message.destination_ref}</strong>
                  <span>{formatMessageTime(message.created_at)} · {message.attempts} 次尝试</span>
                </div>
                <span className={`${styles.deliveryStatus} ${DELIVERY_STATUS_CLASSES[message.status]}`}>
                  {message.status === "sent" ? <Check aria-hidden="true" size={13} /> : null}
                  {statusLabel(message.status)}
                </span>
              </div>
            ))}
          </div>
        ) : <p className={styles.settingsState}>还没有投递记录</p>}
      </section>
    </div>
  );
}
