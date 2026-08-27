"use client";

import { ArrowsLeftRight, CaretDown, Check, X } from "@phosphor-icons/react";
import { AnimatePresence, motion } from "motion/react";
import { useEffect, useRef, useState } from "react";

import type { ConfigSyncStatus } from "../hooks/use-voice-session";
import { getAuthSession } from "../lib/auth-session";
import {
  SUPPORTED_LANGUAGES,
  languageLabel,
  type LanguageCode,
  type InterpretationOutputMode,
  type VoiceSessionConfig,
} from "../lib/languages";
import { hasReadyAutomaticTarget, listSupportedLanguages } from "../lib/lingow-api";
import styles from "../voice.module.css";
import { HistoryPreview, HistorySettings } from "./history-settings";
import { OptionWheel } from "./option-wheel";
import { UsageSettings } from "./usage-settings";
import { DeliverySettings } from "./delivery-settings";
import { AccountSettings } from "./account-settings";

const SETTINGS_ITEMS = [
  {
    id: "language",
    label: "语言配置",
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
    id: "delivery",
    label: "投递管理",
    value: "自动发送",
    description: "绑定目标与查看发送状态",
  },
  {
    id: "account",
    label: "登录状态",
    value: "已登录",
    description: "账户信息与登录管理",
  },
  {
    id: "about",
    label: "关于",
    value: "AI 语音助手",
    description: "Lingow 产品信息",
  },
] as const;

type SettingId = (typeof SETTINGS_ITEMS)[number]["id"];
const HISTORY_INDEX = SETTINGS_ITEMS.findIndex((item) => item.id === "history");

function outputModeLabel(mode: InterpretationOutputMode): string {
  return mode === "single" ? "单向播报" : "双向播报";
}

function outputModeStatusLabel(status: ConfigSyncStatus): string | null {
  switch (status) {
    case "saving":
      return "正在应用到当前会话，下一句开始生效。";
    case "applied":
      return "已应用到当前会话，下一句开始生效。";
    case "failed":
      return "当前会话应用失败，已恢复上一次配置。";
    default:
      return null;
  }
}

const LANGUAGE_TRANSLATIONS: Record<string, string> = {
  "zh-CN": "中文（简体）",
  "en-US": "英语（美国）",
  "ja-JP": "日语",
  "ko-KR": "韩语",
  "fr-FR": "法语",
  "de-DE": "德语",
  "ru-RU": "俄语",
  "pt-BR": "葡萄牙语（巴西）",
  "it-IT": "意大利语",
  "es-ES": "西班牙语",
  "th-TH": "泰语",
  "id-ID": "印度尼西亚语",
  "vi-VN": "越南语",
};

function languageTranslation(code: string): string {
  return LANGUAGE_TRANSLATIONS[code] ?? code;
}

function SelectRow({
  label,
  options,
  labels,
  value,
  onChange,
  open,
  onOpenChange,
}: {
  label: string;
  options: readonly LanguageCode[];
  labels: Readonly<Record<string, string>>;
  value: LanguageCode;
  onChange: (value: LanguageCode) => void;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const fieldRef = useRef<HTMLDivElement>(null);
  const selectedLabel = labels[value] ?? languageLabel(value);

  useEffect(() => {
    if (!open) return;

    const closeOnOutsidePress = (event: PointerEvent) => {
      if (event.target instanceof Node && !fieldRef.current?.contains(event.target)) {
        onOpenChange(false);
      }
    };
    document.addEventListener("pointerdown", closeOnOutsidePress);
    return () => document.removeEventListener("pointerdown", closeOnOutsidePress);
  }, [onOpenChange, open]);

  return (
    <div
      className={styles.languageSelectField}
      onKeyDown={(event) => {
        if (event.key === "Escape" && open) {
          event.preventDefault();
          event.stopPropagation();
          onOpenChange(false);
        }
      }}
      ref={fieldRef}
    >
      <div className={styles.settingSelectRow}>
        <span>{label}</span>
        <button
          aria-expanded={open}
          aria-haspopup="listbox"
          aria-label={`${label}，当前${selectedLabel}`}
          className={styles.languageSelectTrigger}
          onClick={() => onOpenChange(!open)}
          type="button"
        >
          <span>{selectedLabel}</span>
          <code>{value}</code>
          <CaretDown aria-hidden="true" size={15} weight="bold" />
        </button>
      </div>
      {open ? (
        <div aria-label={`${label}选项`} className={styles.languageSelectMenu} role="listbox">
          {options.map((option) => {
            const isSelected = option === value;
            return (
              <button
                aria-selected={isSelected}
                className={`${styles.languageSelectOption} ${isSelected ? styles.languageSelectOptionSelected : ""}`}
                key={option}
                onClick={() => {
                  onChange(option);
                  onOpenChange(false);
                }}
                role="option"
                type="button"
              >
                <span>
                  <strong>{labels[option] ?? languageLabel(option)}</strong>
                  <small>{languageTranslation(option)}</small>
                </span>
                <code>{option}</code>
                {isSelected ? <Check aria-hidden="true" size={15} weight="bold" /> : <i aria-hidden="true" />}
              </button>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}

function SettingsDetail({
  selectedId,
  voiceConfig,
  onConfigChange,
  languageOptions,
  languageLoading,
  languageLabels,
  configSyncStatus,
  onOpenHistory,
  singleOutputReady,
  onLogout,
  logoutDisabled,
}: {
  selectedId: SettingId;
  voiceConfig: VoiceSessionConfig;
  onConfigChange: (next: VoiceSessionConfig) => void;
  languageOptions: readonly LanguageCode[];
  languageLoading: boolean;
  languageLabels: Readonly<Record<string, string>>;
  configSyncStatus: ConfigSyncStatus;
  onOpenHistory: (session: import("../lib/lingow-api").VoiceSession) => void;
  singleOutputReady: boolean | null;
  onLogout?: () => void | Promise<void>;
  logoutDisabled: boolean;
}) {
  const [openLanguage, setOpenLanguage] = useState<"source" | "target" | null>(null);

  switch (selectedId) {
    case "account":
      return <AccountSettings logoutDisabled={logoutDisabled} onLogout={onLogout} />;
    case "language":
      return (
        <div className={styles.languageSettings}>
          <div className={styles.settingRows}>
            <SelectRow
              label="源语言"
              onChange={(sourceLanguage) =>
                onConfigChange({ ...voiceConfig, sourceLanguage })
              }
              options={languageOptions}
              labels={languageLabels}
              onOpenChange={(open) => setOpenLanguage(open ? "source" : null)}
              open={openLanguage === "source"}
              value={voiceConfig.sourceLanguage}
            />
            <SelectRow
              label="目标语言"
              onChange={(targetLanguage) =>
                onConfigChange({ ...voiceConfig, targetLanguage })
              }
              options={languageOptions}
              labels={languageLabels}
              onOpenChange={(open) => setOpenLanguage(open ? "target" : null)}
              open={openLanguage === "target"}
              value={voiceConfig.targetLanguage}
            />
            <div className={styles.settingControlGroup}>
              <span className={styles.settingControlLabel}>译音输出</span>
              <div
                aria-label="译音输出模式"
                className={styles.segmentControl}
                role="group"
              >
                {(["bidirectional", "single"] as const).map((mode) => (
                  <button
                    aria-pressed={voiceConfig.outputMode === mode}
                    className={
                      voiceConfig.outputMode === mode ? styles.segmentActive : ""
                    }
                    disabled={mode === "single" && singleOutputReady !== true}
                    key={mode}
                    onClick={() => onConfigChange({ ...voiceConfig, outputMode: mode })}
                    type="button"
                  >
                    {outputModeLabel(mode)}
                  </button>
                ))}
              </div>
              <p className={styles.settingsState}>
                {voiceConfig.outputMode === "single"
                  ? "当前源语言译文播报；反向译文自动投递，并保留 Final Turn。"
                  : "两种语言的译文都播报；翻译、投递记录和 Final Turn 始终保留。"}
              </p>
              {voiceConfig.outputMode === "single" ? (
                <div className={styles.outputDirectionRow}>
                  <span>播报方向</span>
                  <strong>
                    {languageLabel(voiceConfig.sourceLanguage)} → {languageLabel(voiceConfig.targetLanguage)}
                  </strong>
                  <button
                    aria-label="交换播报方向"
                    className={styles.outputDirectionSwap}
                    onClick={() =>
                      onConfigChange({
                        ...voiceConfig,
                        sourceLanguage: voiceConfig.targetLanguage,
                        targetLanguage: voiceConfig.sourceLanguage,
                      })
                    }
                    title="交换播报方向"
                    type="button"
                  >
                    <ArrowsLeftRight aria-hidden="true" size={16} />
                  </button>
                </div>
              ) : null}
              {singleOutputReady !== true ? (
                <p className={styles.settingsState}>
                  单向播报需要已启用且已验证的自动投递目标。
                </p>
              ) : null}
              {outputModeStatusLabel(configSyncStatus) ? (
                <p className={styles.settingsState}>
                  {outputModeStatusLabel(configSyncStatus)}
                </p>
              ) : null}
            </div>
          </div>
          <p className={styles.languageSettingsNote}>
            {languageLoading
              ? "正在同步 ASR / TTS 支持的语言..."
              : "语言与输出设置会自动保存。活动会话会从下一句开始使用新配置。"}
          </p>
        </div>
      );
    case "history":
      return <HistoryPreview onOpen={onOpenHistory} />;
    case "usage":
      return <UsageSettings />;
    case "delivery":
      return <DeliverySettings />;
    case "about":
      return (
        <div className={styles.aboutView}>
          <div className={styles.aboutMark}>l</div>
          <div>
            <strong>Lingow</strong>
            <span>AI 语音助手 · 面对面同传</span>
          </div>
          <p>
            用自然语音与 Lingow 对话，支持 AI 助手、双向同声传译、实时字幕与语音播报。
          </p>
        </div>
      );
  }
}

export function SettingsPanel({
  onClose,
  voiceConfig,
  onConfigChange,
  configSyncStatus,
  onLogout,
  logoutDisabled = false,
}: {
  onClose: () => void;
  voiceConfig: VoiceSessionConfig;
  onConfigChange: (next: VoiceSessionConfig) => void;
  configSyncStatus: ConfigSyncStatus;
  onLogout?: () => void | Promise<void>;
  logoutDisabled?: boolean;
}) {
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [historySessionId, setHistorySessionId] = useState<string | null>(null);
  const [languageOptions, setLanguageOptions] = useState<LanguageCode[]>(SUPPORTED_LANGUAGES);
  const [languageLoading, setLanguageLoading] = useState(true);
  const [languageLabels, setLanguageLabels] = useState<Record<string, string>>({});
  const [singleOutputReady, setSingleOutputReady] = useState<boolean | null>(null);
  const panelRef = useRef<HTMLElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const selected = SETTINGS_ITEMS[selectedIndex];
  const historyWorkspaceOpen = selected.id === "history" && historySessionId !== null;

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const auth = await getAuthSession();
        const [languagesResult, readinessResult] = await Promise.allSettled([
          listSupportedLanguages(auth.tokens.access_token),
          hasReadyAutomaticTarget(auth.tokens.access_token),
        ]);
        if (languagesResult.status === "fulfilled") {
          const options = languagesResult.value.languages
            .filter((language) => language.supports_as_source && language.supports_as_target)
            .map((language) => language.language_code)
            .filter((code, index, all) => all.indexOf(code) === index);
          if (!cancelled && options.length > 0) {
            setLanguageOptions(options);
            setLanguageLabels(
              Object.fromEntries(
                languagesResult.value.languages.map((language) => [
                  language.language_code,
                  language.display_name || language.display_name_en,
                ]),
              ),
            );
          }
        }
        if (!cancelled) {
          setSingleOutputReady(
            readinessResult.status === "fulfilled" ? readinessResult.value : false,
          );
        }
      } catch {
        // Keep the local catalog available while the API is unavailable.
        if (!cancelled) setSingleOutputReady(false);
      } finally {
        if (!cancelled) setLanguageLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const selectedValue =
    selected.id === "language"
      ? `${languageLabel(voiceConfig.sourceLanguage)} / ${languageLabel(voiceConfig.targetLanguage)} · ${outputModeLabel(voiceConfig.outputMode)}`
      : selected.id === "account"
        ? "当前账户"
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

        <div className={historyWorkspaceOpen ? styles.settingsContentHistory : styles.settingsContent}>
          {!historyWorkspaceOpen ? <section aria-label="设置导航" className={styles.settingsNavigation}>
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
                defaultSelected={selectedIndex}
                fade={0.14}
                fontSize={2.42}
                inset={96}
                items={SETTINGS_ITEMS.map((item) => item.label)}
                onChange={(index) => {
                  setSelectedIndex(index);
                  if (index !== HISTORY_INDEX) setHistorySessionId(null);
                }}
                spacing={1.5}
                tilt={7.2}
              />
            </div>
          </section> : null}

          <section aria-live="polite" className={historyWorkspaceOpen ? styles.settingsDetailHistory : styles.settingsDetail}>
            {historyWorkspaceOpen ? (
              <HistorySettings
                initialSessionId={historySessionId}
                onExit={() => setHistorySessionId(null)}
              />
            ) : <AnimatePresence mode="wait">
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
                    configSyncStatus={configSyncStatus}
                    languageLoading={languageLoading}
                    languageOptions={languageOptions}
                    languageLabels={languageLabels}
                    onConfigChange={onConfigChange}
                    onOpenHistory={(session) => setHistorySessionId(session.id)}
                    logoutDisabled={logoutDisabled}
                    onLogout={onLogout}
                    singleOutputReady={singleOutputReady}
                    selectedId={selected.id}
                    voiceConfig={voiceConfig}
                  />
                </div>
              </motion.div>
            </AnimatePresence>}
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
