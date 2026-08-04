"use client";

import { CaretRight } from "@phosphor-icons/react";
import { motion } from "motion/react";

import type { TranslationTurn } from "../model/session";
import styles from "../voice.module.css";

export function LatestTranslation({ turn, onOpen }: { turn: TranslationTurn; onOpen: () => void }) {
  return (
    <button aria-label="查看完整会话记录" className={styles.latestTranslation} onClick={onOpen} type="button">
      <motion.span animate={{ opacity: 1, y: 0 }} className={styles.language} initial={{ opacity: 0, y: 8 }} key={`${turn.id}-source-language`} transition={{ duration: 0.38, ease: [0.16, 1, 0.3, 1] }}>
        {turn.sourceLanguage}
      </motion.span>
      <motion.span animate={{ opacity: 1, y: 0 }} className={styles.sourceText} initial={{ opacity: 0, y: 9 }} key={`${turn.id}-source`} transition={{ type: "spring", stiffness: 240, damping: 24 }}>
        {turn.source}
      </motion.span>
      <span className={styles.historyCaret} aria-hidden="true"><CaretRight size={15} weight="regular" /></span>
      <motion.span animate={{ opacity: 1, y: 0 }} className={styles.language} initial={{ opacity: 0, y: 8 }} key={`${turn.id}-target-language`} transition={{ delay: 0.07, duration: 0.38, ease: [0.16, 1, 0.3, 1] }}>
        {turn.targetLanguage}
      </motion.span>
      <motion.span animate={{ opacity: 1, y: 0 }} className={styles.translationText} initial={{ opacity: 0, y: 9 }} key={`${turn.id}-translation`} transition={{ delay: 0.07, type: "spring", stiffness: 230, damping: 24 }}>
        {turn.translation}
      </motion.span>
    </button>
  );
}
