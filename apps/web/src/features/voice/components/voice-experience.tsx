"use client";

import { Gear } from "@phosphor-icons/react";
import { AnimatePresence, MotionConfig, motion } from "motion/react";
import { useState } from "react";

import { useVoiceSession } from "../hooks/use-voice-session";
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
    voiceConfig,
    updateConfig,
    debug,
    configSyncStatus,
    toggle,
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
