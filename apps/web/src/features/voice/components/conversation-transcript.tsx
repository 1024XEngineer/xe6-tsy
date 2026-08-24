"use client";

import { AnimatePresence, motion } from "motion/react";
import { useEffect, useRef } from "react";

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
  const activeUtteranceId =
    asrPartial?.turnId ?? phraseSubtitles.at(-1)?.utteranceId ?? null;
  const activePhrases = activeUtteranceId
    ? phraseSubtitles.filter((subtitle) => subtitle.utteranceId === activeUtteranceId)
    : [];
  const phraseSource = activePhrases.map((subtitle) => subtitle.sourceText).join("");
  const phraseTranslation = activePhrases
    .filter((subtitle) => subtitle.status === "translated")
    .map((subtitle) => subtitle.translatedText)
    .join("");
  // ASR partials are the freshest whole-utterance snapshot. Phrase subtitles
  // fill the same row while the snapshot is catching up or unavailable.
  const partialSource = asrPartial?.text || "";
  const source =
    partialSource.length >= phraseSource.length ? partialSource : phraseSource;
  const stash = asrPartial?.stash || "";

  if (!source && !stash && !phraseTranslation) return null;

  return (
    <motion.article
      animate={{ opacity: 1, y: 0 }}
      className={styles.transcriptTurn}
      initial={{ opacity: 0, y: 8 }}
      // Keep one visual live container while phraseSequence updates arrive.
      // translation.final replaces it with a history row in the reducer.
      key="live-utterance"
    >
      {source || stash ? (
        <p className={styles.transcriptSource}>
          {source ? <span className={styles.transcriptConfirmed}>{source}</span> : null}
          {stash ? <span className={styles.transcriptStash}>{stash}</span> : null}
        </p>
      ) : null}
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
  const transcriptRef = useRef<HTMLElement>(null);
  const followLatestRef = useRef(true);

  useEffect(() => {
    const element = transcriptRef.current;
    if (!element || !followLatestRef.current) return;
    if (typeof element.scrollTo === "function") {
      element.scrollTo({ top: element.scrollHeight, behavior: "auto" });
    } else {
      element.scrollTop = element.scrollHeight;
    }
  }, [asrPartial?.text, asrPartial?.stash, finalTurns.length, phraseSubtitles, replies.length]);

  const handleTranscriptScroll = () => {
    const element = transcriptRef.current;
    if (!element) return;
    const distanceFromBottom = element.scrollHeight - element.scrollTop - element.clientHeight;
    followLatestRef.current = distanceFromBottom <= 24;
  };

  return (
    <section
      aria-label={activeMode === "assistant" ? "助手对话记录" : "同声传译记录"}
      className={styles.transcript}
      onScroll={handleTranscriptScroll}
      ref={transcriptRef}
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
                <div className={styles.transcriptPair}>
                  <p className={styles.transcriptSource}>{turn.source}</p>
                  <p className={styles.transcriptTranslation}>{turn.translation}</p>
                </div>
              </motion.article>
            ))
          : replies.map((reply) => (
              <motion.article
                animate={{ opacity: 1, y: 0 }}
                className={styles.transcriptTurn}
                initial={{ opacity: 0, y: 8 }}
                key={reply.replyId}
              >
                <div className={styles.transcriptPair}>
                  {reply.source ? <p className={styles.transcriptSource}>{reply.source}</p> : null}
                  <p className={styles.transcriptAssistant}>{reply.text}</p>
                </div>
              </motion.article>
            ))}
      </AnimatePresence>
      {activeMode === "interpretation" ? (
        <LiveInterpretationTurn asrPartial={asrPartial} phraseSubtitles={phraseSubtitles} />
      ) : null}
    </section>
  );
}
