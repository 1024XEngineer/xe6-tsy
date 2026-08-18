"use client";

import { Gear } from "@phosphor-icons/react";
import { AnimatePresence, MotionConfig, motion } from "motion/react";
import { useState } from "react";

import { useVoiceSession } from "../hooks/use-voice-session";
import styles from "../voice.module.css";
import { HistoryOverlay } from "./history-overlay";
import { LatestAssistantReply } from "./latest-assistant-reply";
import { LatestTranslation } from "./latest-translation";
import { SettingsPanel } from "./settings-panel";
import { VoiceControl } from "./voice-control";

export function VoiceExperience() {
  const {
    state,
    transientASRSubtitle,
    latestTurn,
    latestAssistantReply,
    activeMode,
    statusMessage,
    hintMessage,
    automaticOutputMessage,
    voiceConfig,
    updateConfig,
    debug,
    configSyncStatus,
    commandFeedback,
    interactionPolicy,
    interactionPolicyLocked,
    setInteractionPolicy,
    switchMode,
    toggle,
  } = useVoiceSession();
  const [historyOpen, setHistoryOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const hasVisibleLatest =
    activeMode === "assistant" ? Boolean(latestAssistantReply) : Boolean(latestTurn);

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
            y: state.phase === "active" && hasVisibleLatest ? "-9dvh" : 0,
          }}
          className={styles.voiceStage}
          transition={{ type: "spring", stiffness: 110, damping: 21 }}
        >
          <VoiceControl mode={activeMode} phase={state.phase} onActivate={handleToggle} />
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
          {state.phase !== "idle" ? (
            <div
              aria-label="对话监听方式"
              className={styles.interactionPolicyControls}
              role="group"
            >
              <button
                aria-pressed={interactionPolicy === "continuous"}
                onClick={() => setInteractionPolicy("continuous")}
                type="button"
              >
                常驻模式
              </button>
              <button
                aria-pressed={interactionPolicy === "wake_word"}
                disabled={interactionPolicyLocked}
                onClick={() => setInteractionPolicy("wake_word")}
                title={
                  interactionPolicyLocked
                    ? "同声传译保持常驻上行，唤醒词仍可用于退出指令"
                    : undefined
                }
                type="button"
              >
                唤醒词模式
              </button>
            </div>
          ) : null}
          {automaticOutputMessage ? (
            <p className={styles.automaticOutputText} role="status">
              {automaticOutputMessage}
            </p>
          ) : null}
          {commandFeedback ? (
            <p
              className={styles.commandFeedback}
              data-status={commandFeedback.status}
              role="status"
            >
              {commandFeedback.message}
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
          {transientASRSubtitle ? (
            <p aria-label="临时识别结果" aria-live="polite" className={styles.transientASRSubtitle}>
              {transientASRSubtitle.text}
            </p>
          ) : null}
        </motion.section>

        {!historyOpen && activeMode === "assistant" && latestAssistantReply ? (
          <div className={styles.latestSlot}>
            <LatestAssistantReply reply={latestAssistantReply} />
          </div>
        ) : null}
        {!historyOpen && activeMode === "interpretation" && latestTurn ? (
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
