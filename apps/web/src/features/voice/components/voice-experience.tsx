"use client";

import { ArrowsLeftRight, Gear, Robot, SpeakerHigh } from "@phosphor-icons/react";
import { AnimatePresence, MotionConfig, motion } from "motion/react";
import { useState } from "react";

import { useVoiceSession } from "../hooks/use-voice-session";
import styles from "../voice.module.css";
import { ConversationTranscript } from "./conversation-transcript";
import { LiquidStatusOrb } from "./liquid-status-orb";
import { SettingsPanel } from "./settings-panel";
import { VoiceControl } from "./voice-control";

type VoiceExperienceProps = {
  onLogout?: () => void | Promise<void>;
};

export function VoiceExperience({ onLogout }: VoiceExperienceProps = {}) {
  const {
    state,
    transientASRSubtitle,
    transientPhraseSubtitles,
    activeMode,
    statusMessage,
    hintMessage,
    automaticOutputMessage,
    voiceConfig,
    updateConfig,
    debug,
    configSyncStatus,
    commandFeedback,
    switchMode,
    toggle,
  } = useVoiceSession();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [modeMenuOpen, setModeMenuOpen] = useState(false);
  const modeSwitching =
    debug.modeCommandPending || debug.modeState?.phase === "switching";
  const capsuleStatus = activeMode === "assistant" ? "AI 助手模式" : "同声传译模式";
  const statusTone = state.notice || debug.lastError || debug.connectionState === "failed"
    ? "error"
    : modeSwitching
      ? "switching"
      : "ready";

  const handleToggle = () => {
    setSettingsOpen(false);
    toggle();
  };

  const openSettings = () => {
    setSettingsOpen(true);
  };

  const handleModeSwitch = (mode: "assistant" | "interpretation") => {
    setModeMenuOpen(false);
    void switchMode(mode);
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

        {state.phase !== "idle" ? <section className={styles.statusCluster} aria-label="会话状态与模式">
          <div className={`${styles.statusCapsule} ${modeMenuOpen ? styles.statusCapsuleExpanded : ""}`}>
            <button
              aria-controls="mode-switch-menu"
              aria-expanded={modeMenuOpen}
              aria-haspopup="menu"
              aria-label="切换工作模式"
              className={styles.statusCapsuleTrigger}
              disabled={modeSwitching}
              onClick={() => setModeMenuOpen((open) => !open)}
              type="button"
            >
              <LiquidStatusOrb status={statusTone} />
              <span className={styles.statusCapsuleBody}>
                <span aria-live="polite" className={styles.capsuleStatus}>
                  {capsuleStatus}
                </span>
                <span aria-live="polite" className={styles.srOnly}>
                  {statusMessage}
                </span>
              </span>
              <ArrowsLeftRight aria-hidden="true" className={styles.modeMenuIcon} size={19} weight="regular" />
            </button>
            <AnimatePresence initial={false}>
              {modeMenuOpen ? (
                <motion.div
                  animate={{ height: "auto", opacity: 1, y: 0 }}
                  className={styles.modeMenu}
                  exit={{ height: 0, opacity: 0, y: -4 }}
                  id="mode-switch-menu"
                  initial={{ height: 0, opacity: 0, y: -4 }}
                  role="menu"
                  transition={{ duration: 0.24, ease: [0.16, 1, 0.3, 1] }}
                >
                  {(["assistant", "interpretation"] as const).map((mode) => (
                    <button
                      aria-checked={activeMode === mode}
                      key={mode}
                      onClick={() => handleModeSwitch(mode)}
                      role="menuitemradio"
                      type="button"
                    >
                      {mode === "assistant" ? (
                        <Robot aria-hidden="true" size={19} weight="regular" />
                      ) : (
                        <SpeakerHigh aria-hidden="true" size={19} weight="regular" />
                      )}
                      <span>{mode === "assistant" ? "AI 助手模式" : "同声传译模式"}</span>
                    </button>
                  ))}
                </motion.div>
              ) : null}
            </AnimatePresence>
          </div>
          <div className={styles.statusHints}>
            {hintMessage ? <p>{hintMessage}</p> : null}
            {automaticOutputMessage ? <p>{automaticOutputMessage}</p> : null}
            {commandFeedback ? <p>{commandFeedback.message}</p> : null}
          </div>
        </section> : null}
        {state.phase === "idle" ? (
          <>
            <p aria-live="polite" className={styles.idleStatus}>
              {statusMessage}
            </p>
            {hintMessage ? (
              <p aria-live="polite" className={styles.idleHint} role="status">
                {hintMessage}
              </p>
            ) : null}
          </>
        ) : null}

        <section className={styles.voiceStage}>
          <VoiceControl mode={activeMode} phase={state.phase} onActivate={handleToggle} />
        </section>

        <ConversationTranscript
          activeMode={activeMode}
          assistantReplies={state.assistantReplies}
          asrPartial={transientASRSubtitle}
          phraseSubtitles={transientPhraseSubtitles}
          turns={state.turns}
        />

        <AnimatePresence>
          {settingsOpen ? (
            <SettingsPanel
              configSyncStatus={configSyncStatus}
              logoutDisabled={state.phase !== "idle"}
              onClose={() => setSettingsOpen(false)}
              onConfigChange={updateConfig}
              onLogout={onLogout}
              voiceConfig={voiceConfig}
            />
          ) : null}
        </AnimatePresence>
      </main>
    </MotionConfig>
  );
}
