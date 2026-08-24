"use client";

import { ArrowsLeftRight, CaretDown, Gear } from "@phosphor-icons/react";
import { AnimatePresence, MotionConfig, motion } from "motion/react";
import { useState } from "react";

import { useVoiceSession } from "../hooks/use-voice-session";
import styles from "../voice.module.css";
import { ConversationTranscript } from "./conversation-transcript";
import { LiquidStatusOrb } from "./liquid-status-orb";
import { SettingsPanel } from "./settings-panel";
import { VoiceControl } from "./voice-control";

export function VoiceExperience() {
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
    interactionPolicy,
    interactionPolicyLocked,
    setInteractionPolicy,
    switchMode,
    toggle,
  } = useVoiceSession();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [modeMenuOpen, setModeMenuOpen] = useState(false);
  const modeSwitching =
    debug.modeCommandPending || debug.modeState?.phase === "switching";
  const modeLabel = activeMode === "assistant" ? "AI 助手" : "同声传译";
  const policyLabel = interactionPolicy === "continuous" ? "常驻模式" : "唤醒词模式";
  const capsuleStatus = modeSwitching ? "切换中…" : `${modeLabel} · ${policyLabel}`;
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

  const toggleInteractionPolicy = () => {
    setInteractionPolicy(interactionPolicy === "continuous" ? "wake_word" : "continuous");
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
          <div className={styles.statusCapsule}>
            <LiquidStatusOrb status={statusTone} />
            <div className={styles.statusCapsuleBody}>
              <p aria-live="polite" className={styles.capsuleStatus}>
                {capsuleStatus}
              </p>
              <p className={styles.capsuleDetail}>{statusMessage}</p>
            </div>
            <div className={styles.modeMenuWrap}>
              <button
                aria-controls="mode-switch-menu"
                aria-expanded={modeMenuOpen}
                aria-label="切换工作模式"
                className={styles.modeMenuTrigger}
                disabled={modeSwitching}
                onClick={() => setModeMenuOpen((open) => !open)}
                type="button"
              >
                <ArrowsLeftRight aria-hidden="true" size={17} weight="regular" />
                <CaretDown aria-hidden="true" size={12} weight="bold" />
              </button>
              {modeMenuOpen ? (
                <div className={styles.modeMenu} id="mode-switch-menu" role="menu">
                  {(["assistant", "interpretation"] as const).map((mode) => (
                    <button
                      aria-checked={activeMode === mode}
                      key={mode}
                      onClick={() => handleModeSwitch(mode)}
                      role="menuitemradio"
                      type="button"
                    >
                      {mode === "assistant" ? "AI 助手" : "同声传译"}
                    </button>
                  ))}
                </div>
              ) : null}
            </div>
          </div>
          <button
            aria-checked={interactionPolicy === "wake_word"}
            aria-label={`监听方式：${policyLabel}`}
            className={styles.interactionToggle}
            disabled={interactionPolicyLocked}
            onClick={toggleInteractionPolicy}
            role="switch"
            title={
              "切换常驻模式或唤醒词模式"
            }
            type="button"
          >
            <span>{policyLabel}</span>
            <i aria-hidden="true" />
          </button>
          <div className={styles.statusHints}>
            {hintMessage ? <p>{hintMessage}</p> : null}
            {automaticOutputMessage ? <p>{automaticOutputMessage}</p> : null}
            {commandFeedback ? <p>{commandFeedback.message}</p> : null}
          </div>
        </section> : null}
        {state.phase === "idle" ? (
          <p aria-live="polite" className={styles.idleStatus}>
            {statusMessage}
          </p>
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
