"use client";

import { Gear } from "@phosphor-icons/react";
import { AnimatePresence, MotionConfig, motion } from "motion/react";
import { useState } from "react";

import { useVoiceSession } from "../hooks/use-voice-session";
import { formatActivePair } from "../lib/languages";
import styles from "../voice.module.css";
import { HistoryOverlay } from "./history-overlay";
import { LatestTranslation } from "./latest-translation";
import { SettingsPanel } from "./settings-panel";
import { VoiceControl } from "./voice-control";

export function VoiceExperience() {
  const {
    state,
    latestTurn,
    statusMessage,
    hintMessage,
    automaticOutputMessage,
    voiceConfig,
    updateConfig,
    debug,
    configSyncStatus,
    switchMode,
    toggle,
    wakeStatus,
  } = useVoiceSession();
  const [historyOpen, setHistoryOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);

  const handleToggle = () => {
    setSettingsOpen(false);
    if (state.phase !== "idle") setHistoryOpen(false);
    toggle();
  };

  const openSettings = () => {
    setHistoryOpen(false);
    setSettingsOpen(true);
  };

  return (
    <MotionConfig reducedMotion="user">
      <main className={styles.experience} data-phase={state.phase}>
        <motion.header
          animate={{ opacity: 1, y: 0 }}
          className={styles.brandHeader}
          initial={{ opacity: 0, y: -8 }}
          transition={{ duration: 0.58, ease: [0.16, 1, 0.3, 1] }}
        >
          <h1 className={styles.wordmark} translate="no">
            lingow
          </h1>
          <button
            aria-controls="settings-panel"
            aria-expanded={settingsOpen}
            aria-label="设置"
            className={styles.iconButton}
            onClick={openSettings}
            title="设置"
            type="button"
          >
            <Gear aria-hidden="true" size={19} weight="regular" />
          </button>
        </motion.header>

        <motion.section
          animate={{
            y: state.phase === "active" && latestTurn ? "-9dvh" : 0,
          }}
          className={styles.voiceStage}
          transition={{ type: "spring", stiffness: 110, damping: 21 }}
        >
          <VoiceControl phase={state.phase} onActivate={handleToggle} />
          <p aria-live="polite" className={styles.outputModeText}>
            {formatActivePair(voiceConfig)}
          </p>
          {state.phase !== "idle" ? (
            <div aria-label="实时状态" className={styles.runtimeStatus}>
              <span>连接：{debug.connectionState ?? "未知"}</span>
              <span>Runtime：{debug.runtimeState ?? "未知"}</span>
              <span>
                Mode：{debug.modeState?.active_mode ?? "传统同传"}
              </span>
            </div>
          ) : null}
          {debug.modeState ? (
            <div aria-label="模式切换" className={styles.modeControls} role="group">
              {(["assistant", "interpretation"] as const).map((mode) => (
                <button
                  aria-pressed={debug.modeState?.active_mode === mode}
                  disabled={debug.modeCommandPending || debug.modeState?.phase === "switching"}
                  key={mode}
                  onClick={() => void switchMode(mode)}
                  type="button"
                >
                  {mode === "assistant" ? "AI 助手" : "同声传译"}
                </button>
              ))}
              {debug.modeCommandPending || debug.modeState?.phase === "switching" ? (
                <span role="status">模式切换中…</span>
              ) : null}
            </div>
          ) : null}
          {automaticOutputMessage ? (
            <p className={styles.automaticOutputText} role="status">
              {automaticOutputMessage}
            </p>
          ) : null}
          <motion.p
            animate={{ opacity: 1, y: 0 }}
            aria-live="polite"
            className={styles.statusText}
            initial={{ opacity: 0, y: 6 }}
            key={statusMessage}
            transition={{
              delay: 0.04,
              type: "spring",
              stiffness: 260,
              damping: 24,
            }}
          >
            {statusMessage}
          </motion.p>
          {hintMessage ? (
            <motion.p
              animate={{ opacity: 1, y: 0 }}
              className={styles.hintText}
              initial={{ opacity: 0, y: 4 }}
              key={hintMessage}
              transition={{ duration: 0.28, ease: [0.16, 1, 0.3, 1] }}
            >
              {hintMessage}
            </motion.p>
          ) : null}
          {state.phase === "idle" && wakeStatus === "listening" ? (
            <p className={styles.commandWindowNotice} role="status">
              有界语音命令窗口暂不可用，当前仅支持“开始/停止翻译”兼容唤醒词。
            </p>
          ) : null}
        </motion.section>

        {latestTurn && !historyOpen ? (
          <div className={styles.latestSlot}>
            <LatestTranslation
              onOpen={() => setHistoryOpen(true)}
              turn={latestTurn}
            />
          </div>
        ) : null}

        {historyOpen ? (
          <div className={styles.overlaySlot}>
            <HistoryOverlay
              onClose={() => setHistoryOpen(false)}
              turns={state.turns}
            />
          </div>
        ) : null}

        <AnimatePresence>
          {settingsOpen ? (
            <SettingsPanel
              debug={debug}
              configSyncStatus={configSyncStatus}
              onClose={() => setSettingsOpen(false)}
              onConfigChange={updateConfig}
              voiceConfig={voiceConfig}
            />
          ) : null}
        </AnimatePresence>
      </main>
    </MotionConfig>
  );
}
