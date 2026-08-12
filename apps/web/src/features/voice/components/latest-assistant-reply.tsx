"use client";

import { motion } from "motion/react";

import type { AssistantReply } from "../model/session";
import styles from "../voice.module.css";

export function LatestAssistantReply({ reply }: { reply: AssistantReply }) {
  return (
    <article aria-label="助手回复" className={styles.latestAssistantReply}>
      <motion.span
        animate={{ opacity: 1, y: 0 }}
        className={styles.assistantReplyLabel}
        initial={{ opacity: 0, y: 8 }}
        transition={{ duration: 0.3 }}
      >
        助手
      </motion.span>
      <motion.p
        animate={{ opacity: 1, y: 0 }}
        aria-live="polite"
        className={styles.assistantReplyText}
        initial={{ opacity: 0, y: 9 }}
        key={reply.replyId}
        transition={{ type: "spring", stiffness: 230, damping: 24 }}
      >
        {reply.text}
      </motion.p>
    </article>
  );
}
