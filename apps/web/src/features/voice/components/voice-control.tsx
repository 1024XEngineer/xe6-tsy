"use client";

import { motion } from "motion/react";

import type { VoiceInitialMode } from "../lib/lingow-api";
import type { SessionPhase } from "../model/session";
import styles from "../voice.module.css";
import { AuroraStrands } from "./aurora-strands";
import { VideoOrb } from "./video-orb";

export function VoiceControl({
  phase,
  onActivate,
  mode,
}: {
  phase: SessionPhase;
  onActivate: () => void;
  mode: VoiceInitialMode;
}) {
  const isIdle = phase === "idle";

  return (
    <motion.button
      aria-label={
        isIdle
          ? mode === "assistant"
            ? "开始对话"
            : "开始翻译"
          : mode === "assistant"
            ? "停止对话"
            : "停止翻译"
      }
      className={styles.voiceButton}
      onClick={onActivate}
      type="button"
      whileTap={{ scale: 0.98 }}
    >
      {isIdle ? (
        <motion.span
          animate={{ opacity: 1, scale: 1 }}
          className={styles.voiceVisual}
          data-testid="idle-voice-ring"
          initial={{ opacity: 1, scale: 0.9 }}
          key="idle"
          transition={{ duration: 0.32, ease: [0.16, 1, 0.3, 1] }}
        >
          <VideoOrb />
        </motion.span>
      ) : (
        <motion.span
          animate={{ opacity: 1, scaleX: 1 }}
          className={styles.voiceVisual}
          data-testid="active-voice-strands"
          initial={{ opacity: 0, scaleX: 0.24 }}
          key="active"
          transition={{ duration: 0.48, ease: [0.16, 1, 0.3, 1] }}
        >
          <AuroraStrands />
        </motion.span>
      )}
    </motion.button>
  );
}
