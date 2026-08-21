"use client";

import { AnimatePresence, motion } from "motion/react";

import type {
  AssistantReply,
  TransientASRSubtitle,
  TransientPhraseSubtitle,
  TranslationTurn,
} from "../model/session";
import styles from "../voice.module.css";

const transcriptLimit = 12;

type Props = {
  activeMode: "assistant" | "interpretation";
  assistantReplies: AssistantReply[];
  asrPartial: TransientASRSubtitle | null;
  phraseSubtitles: TransientPhraseSubtitle[];
  turns: TranslationTurn[];
};

function LiveInterpretationTurn({
  asrPartial,
  phraseSubtitles,
}: Pick<Props, "asrPartial" | "phraseSubtitles">) {
  const phraseSource = phraseSubtitles.map((subtitle) => subtitle.sourceText).join("");
  const phraseTranslation = phraseSubtitles
    .filter((subtitle) => subtitle.status === "translated")
    .map((subtitle) => subtitle.translatedText)
    .join("");
  const source = asrPartial?.text || phraseSource;

  if (!source && !phraseTranslation) return null;

  return (
    <motion.article
      animate={{ opacity: 1, y: 0 }}
      className={styles.transcriptTurn}
      initial={{ opacity: 0, y: 8 }}
      key={asrPartial?.turnId ?? phraseSubtitles.at(-1)?.utteranceId ?? "live"}
    >
      {source ? <p className={styles.transcriptSource}>{source}</p> : null}
      {phraseTranslation ? (
        <p className={styles.transcriptTranslation}>{phraseTranslation}</p>
      ) : null}
    </motion.article>
  );
}

export function ConversationTranscript({
  activeMode,
  assistantReplies,
  asrPartial,
  phraseSubtitles,
  turns,
}: Props) {
  const finalTurns = turns.slice(-transcriptLimit);
  const replies = assistantReplies.slice(-transcriptLimit);

  return (
    <section
      aria-label={activeMode === "assistant" ? "助手对话记录" : "同声传译记录"}
      className={styles.transcript}
    >
      <AnimatePresence initial={false} mode="popLayout">
        {activeMode === "interpretation"
          ? finalTurns.map((turn) => (
              <motion.article
                animate={{ opacity: 1, y: 0 }}
                className={styles.transcriptTurn}
                initial={{ opacity: 0, y: 8 }}
                key={turn.id}
              >
                <p className={styles.transcriptSource}>{turn.source}</p>
                <p className={styles.transcriptTranslation}>{turn.translation}</p>
              </motion.article>
            ))
          : replies.map((reply) => (
              <motion.article
                animate={{ opacity: 1, y: 0 }}
                className={styles.transcriptTurn}
                initial={{ opacity: 0, y: 8 }}
                key={reply.replyId}
              >
                {reply.source ? <p className={styles.transcriptSource}>{reply.source}</p> : null}
                <p className={styles.transcriptAssistant}>{reply.text}</p>
              </motion.article>
            ))}
      </AnimatePresence>
      {activeMode === "interpretation" ? (
        <LiveInterpretationTurn asrPartial={asrPartial} phraseSubtitles={phraseSubtitles} />
      ) : null}
    </section>
  );
}
