"use client";

import { Check, X } from "@phosphor-icons/react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useRef, useState } from "react";

import type { SessionDebugInfo } from "../hooks/use-voice-session";
import {
  SUPPORTED_LANGUAGES,
  languageLabel,
  type LanguageCode,
  type VoiceSessionConfig,
} from "../lib/languages";
import styles from "../voice.module.css";
import { HistorySettings } from "./history-settings";
import { OptionWheel } from "./option-wheel";
import { UsageSettings } from "./usage-settings";

const SETTINGS_ITEMS = [
  {
    id: "language",
    label: "默认语言对",
    value: "zh-CN / en-US",
    description: "下次会话使用的双向语言",
  },
  {
    id: "history",
    label: "历史会话",
    value: "查看记录",
    description: "按会话查看翻译记录",
  },
  {
    id: "usage",
    label: "用量管理",
    value: "本月分钟数",
    description: "查看本月免费用量",
  },
  {
    id: "session",
    label: "联调会话",
    value: "调试信息",
    description: "account / session / runtime",
  },
  {
    id: "about",
    label: "关于",
    value: "Lingow 联调前端",
    description: "对接 xe6-tsy 正式协议",
  },
] as const;

type SettingId = (typeof SETTINGS_ITEMS)[number]["id"];

function SelectRow({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: readonly LanguageCode[];
  value: LanguageCode;
  onChange: (value: LanguageCode) => void;
}) {
  return (
    <label className={styles.settingSelectRow}>
      <span>{label}</span>
      <select
        value={value}
        onChange={(event) => onChange(event.target.value as LanguageCode)}
      >
        {options.map((option) => (
          <option key={option} value={option}>
            {languageLabel(option)} ({option})
          </option>
        ))}
      </select>
    </label>
  );
}

function SettingsDetail({
  selectedId,
  voiceConfig,
  onConfigChange,
  debug,
}: {
  selectedId: SettingId;
  voiceConfig: VoiceSessionConfig;
  onConfigChange: (next: VoiceSessionConfig) => void;
  debug: SessionDebugInfo;
}) {
  switch (selectedId) {
    case "language":
      return (
        <div className={styles.settingRows}>
          <SelectRow
            label="源语言"
            onChange={(sourceLanguage) =>
              onConfigChange({ ...voiceConfig, sourceLanguage })
            }
            options={SUPPORTED_LANGUAGES}
            value={voiceConfig.sourceLanguage}
          />
          <SelectRow
            label="目标语言"
            onChange={(targetLanguage) =>
              onConfigChange({ ...voiceConfig, targetLanguage })
            }
            options={SUPPORTED_LANGUAGES}
            value={voiceConfig.targetLanguage}
          />
          <p>
            会写入双向 language-configs（互为逆方向）。下次开始会话生效。
          </p>
        </div>
      );
    case "session":
      return (
        <div className={styles.aboutView}>
          <div>
            <strong>Account</strong>
            <span>{debug.accountId ?? "—"}</span>
          </div>
          <div>
            <strong>Session</strong>
            <span>{debug.sessionId ?? "—"}</span>
          </div>
          <div>
            <strong>Runtime</strong>
            <span>{debug.runtimeState ?? "—"}</span>
          </div>
          <div>
            <strong>Last error</strong>
            <span>{debug.lastError ?? "—"}</span>
          </div>
        </div>
      );
    case "history":
      return <HistorySettings />;
    case "usage":
      return <UsageSettings />;
    case "about":
      return (
        <div className={styles.aboutView}>
          <div className={styles.aboutMark}>l</div>
          <div>
            <strong>Lingow 联调前端</strong>
            <span>xe6-tsy /api/v1 + /realtime/v1</span>
          </div>
          <p>
            匿名登录 → 建会话 → 语言配置 → 本地签发 ticket → WebRTC → Start。
            不含 Python 半双工后端。
          </p>
        </div>
      );
  }
}

export function SettingsPanel({
  onClose,
  voiceConfig,
  onConfigChange,
  debug,
}: {
  onClose: () => void;
  voiceConfig: VoiceSessionConfig;
  onConfigChange: (next: VoiceSessionConfig) => void;
  debug: SessionDebugInfo;
}) {
  const [selectedIndex, setSelectedIndex] = useState(0);
  const panelRef = useRef<HTMLElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const selected = SETTINGS_ITEMS[selectedIndex];

  const selectedValue =
    selected.id === "language"
      ? `${languageLabel(voiceConfig.sourceLanguage)} / ${languageLabel(voiceConfig.targetLanguage)}`
      : selected.id === "session"
        ? debug.sessionId
          ? debug.sessionId.slice(0, 18)
          : "未开始"
        : selected.value;

  useEffect(() => {
    closeButtonRef.current?.focus();

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
        return;
      }

      if (event.key !== "Tab" || !panelRef.current) return;
      const focusable = Array.from(
        panelRef.current.querySelectorAll<HTMLElement>(
          'button:not([disabled]), select:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      );
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];

      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  return (
    <motion.div
      animate={{ opacity: 1 }}
      className={styles.settingsLayer}
      exit={{ opacity: 0 }}
      initial={{ opacity: 0 }}
      transition={{ duration: 0.3, ease: [0.16, 1, 0.3, 1] }}
    >
      <div aria-hidden="true" className={styles.settingsBackdrop} />
      <motion.aside
        animate={{ x: 0 }}
        aria-label="设置"
        aria-modal="true"
        className={styles.settingsPanel}
        exit={{ x: "-100%" }}
        id="settings-panel"
        initial={{ x: "-100%" }}
        ref={panelRef}
        role="dialog"
        transition={{ duration: 0.62, ease: [0.16, 1, 0.3, 1] }}
      >
        <header className={styles.settingsHeader}>
          <div className={styles.settingsTitle}>
            <span>lingow</span>
            <span aria-hidden="true" />
            <strong>设置</strong>
          </div>
          <button
            aria-label="关闭设置"
            className={styles.iconButton}
            onClick={onClose}
            ref={closeButtonRef}
            type="button"
          >
            <X aria-hidden="true" size={20} weight="regular" />
          </button>
        </header>

        <div className={styles.settingsContent}>
          <section aria-label="设置导航" className={styles.settingsNavigation}>
            <div className={styles.settingsCount}>
              <span>{String(selectedIndex + 1).padStart(2, "0")}</span>
              <i />
              <span>{String(SETTINGS_ITEMS.length).padStart(2, "0")}</span>
            </div>
            <div className={styles.settingsWheelShell}>
              <span aria-hidden="true" className={styles.settingsMarker} />
              <OptionWheel
                blur={0.58}
                curve={0.68}
                fade={0.14}
                fontSize={2.42}
                inset={96}
                items={SETTINGS_ITEMS.map((item) => item.label)}
                onChange={(index) => setSelectedIndex(index)}
                spacing={1.5}
                tilt={7.2}
              />
            </div>
          </section>

          <section aria-live="polite" className={styles.settingsDetail}>
            <AnimatePresence mode="wait">
              <motion.div
                animate={{ opacity: 1, y: 0 }}
                className={styles.settingsDetailInner}
                exit={{ opacity: 0, y: -10 }}
                initial={{ opacity: 0, y: 14 }}
                key={selected.id}
                transition={{ duration: 0.32, ease: [0.16, 1, 0.3, 1] }}
              >
                <div className={styles.settingsDetailHeading}>
                  <span>{selected.description}</span>
                  <h2>{selected.label}</h2>
                  <p>{selectedValue}</p>
                </div>
                <div className={styles.settingsDetailControls}>
                  <SettingsDetail
                    debug={debug}
                    onConfigChange={onConfigChange}
                    selectedId={selected.id}
                    voiceConfig={voiceConfig}
                  />
                </div>
              </motion.div>
            </AnimatePresence>
          </section>
        </div>

        <footer className={styles.settingsFooter}>
          <span>Lingow OS</span>
          <span className={styles.settingsSaved}>
            <Check aria-hidden="true" size={12} />
            设置自动保存
          </span>
          <span>xe6-tsy</span>
        </footer>
      </motion.aside>
    </motion.div>
  );
}
