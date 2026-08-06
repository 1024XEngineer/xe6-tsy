"use client";

import { X } from "@phosphor-icons/react";
import { motion } from "motion/react";
import { useEffect, useRef } from "react";

import type { TranslationTurn } from "../model/session";
import styles from "../voice.module.css";

export function HistoryOverlay({ turns, onClose }: { turns: TranslationTurn[]; onClose: () => void }) {
  const closeButton = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    closeButton.current?.focus();
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
      if (event.key === "Tab") {
        event.preventDefault();
        closeButton.current?.focus();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  return (
    <motion.section animate={{ opacity: 1 }} aria-label="会话记录" aria-modal="true" className={styles.historyOverlay} initial={{ opacity: 0 }} role="dialog" transition={{ duration: 0.28, ease: [0.16, 1, 0.3, 1] }}>
      <motion.header animate={{ opacity: 1, y: 0 }} className={styles.historyHeader} initial={{ opacity: 0, y: 10 }} transition={{ delay: 0.06, duration: 0.4, ease: [0.16, 1, 0.3, 1] }}>
        <div><h2>会话记录</h2><p>中文 / English</p></div>
        <button aria-label="关闭会话记录" className={styles.iconButton} onClick={onClose} ref={closeButton} type="button"><X aria-hidden="true" size={20} weight="regular" /></button>
      </motion.header>
      <div className={styles.historyScroller}>
        {turns.length === 0 ? (
          <p className={styles.emptyHistory}>还没有翻译记录</p>
        ) : (
          turns.map((turn) => (
            <motion.article animate={{ opacity: 1, y: 0 }} className={styles.historyTurn} data-testid="history-turn" initial={{ opacity: 0, y: 12 }} key={turn.id} transition={{ delay: 0.1 + Math.min(0.24, turns.indexOf(turn) * 0.055), duration: 0.42, ease: [0.16, 1, 0.3, 1] }}>
              <span>{turn.sourceLanguage}</span>
              <div><p>{turn.source}</p><p>{turn.translation}</p></div>
            </motion.article>
          ))
        )}
      </div>
    </motion.section>
  );
}
