export type SessionPhase =
  | "idle"
  | "listening"
  | "processing"
  | "playing"
  | "active";
export type AudioMode = "microphone" | "simulated" | null;

export type TranslationTurn = {
  id: string;
  sourceLanguage: string;
  targetLanguage: string;
  source: string;
  translation: string;
};

export type AssistantReply = {
  replyId: string;
  turnId: string;
  source?: string;
  text: string;
  language: string;
};

export type TransientASRSubtitle = {
  turnId: string;
  text: string;
  stash?: string;
  sourceLanguage: string;
};

export type TransientPhraseSubtitle = {
  utteranceId: string;
  phraseSequence: number;
  sourceText: string;
  translatedText: string;
  status: "source_stable" | "translated" | "translation_failed";
};

export type SessionState = {
  phase: SessionPhase;
  audioMode: AudioMode;
  notice: string | null;
  turns: TranslationTurn[];
  assistantReplies: AssistantReply[];
  asrPartial: TransientASRSubtitle | null;
  phraseSubtitles: TransientPhraseSubtitle[];
};

export const initialSession: SessionState = {
  phase: "idle",
  audioMode: null,
  notice: null,
  turns: [],
  assistantReplies: [],
  asrPartial: null,
  phraseSubtitles: [],
};

export type SessionEvent =
  | { type: "START" }
  | { type: "ACTIVATE" }
  | { type: "PROCESSING" }
  | { type: "PLAYING" }
  | { type: "SET_TURNS"; turns: TranslationTurn[] }
  | { type: "ADD_TURN"; turn: TranslationTurn }
  | { type: "ADD_ASSISTANT_REPLY"; reply: AssistantReply }
  | { type: "SET_ASR_PARTIAL"; partial: TransientASRSubtitle }
  | { type: "ADD_PHRASE_SUBTITLE"; subtitle: TransientPhraseSubtitle }
  | { type: "CLEAR_ASR_PARTIAL" }
  | { type: "FALLBACK"; message: string }
  | { type: "ERROR"; message: string }
  | { type: "END" };

function mergeTurns(
  local: TranslationTurn[],
  remote: TranslationTurn[],
): TranslationTurn[] {
  if (remote.length === 0) {
    return local;
  }
  const byId = new Map<string, TranslationTurn>();
  for (const turn of local) {
    byId.set(turn.id, turn);
  }
  for (const turn of remote) {
    byId.set(turn.id, turn);
  }
  const ordered: TranslationTurn[] = [];
  const seen = new Set<string>();
  for (const turn of local) {
    const next = byId.get(turn.id);
    if (next && !seen.has(turn.id)) {
      ordered.push(next);
      seen.add(turn.id);
    }
  }
  for (const turn of remote) {
    if (!seen.has(turn.id)) {
      ordered.push(turn);
      seen.add(turn.id);
    }
  }
  return ordered;
}

function clearTransientSubtitlesForTurns(
  state: SessionState,
  turnIds: ReadonlySet<string>,
): SessionState {
  const asrPartial =
    state.asrPartial && turnIds.has(state.asrPartial.turnId)
      ? null
      : state.asrPartial;
  const phraseSubtitles = state.phraseSubtitles.filter(
    (subtitle) => !turnIds.has(subtitle.utteranceId),
  );
  if (asrPartial === state.asrPartial && phraseSubtitles.length === state.phraseSubtitles.length) {
    return state;
  }
  return { ...state, asrPartial, phraseSubtitles };
}

export function sessionReducer(
  state: SessionState,
  event: SessionEvent,
): SessionState {
  switch (event.type) {
    case "START":
      return { ...initialSession, phase: "listening", audioMode: "microphone" };
    case "ACTIVATE":
      return { ...state, phase: "active", notice: null };
    case "PROCESSING":
      return { ...state, phase: "processing", notice: null };
    case "PLAYING":
      return { ...state, phase: "playing", notice: null };
    case "SET_TURNS": {
      // Poll may return [] while FinalTurns only arrive on DataChannel (no API
      // outbox yet). Merge so remote never wipes locally observed subtitles.
      const polledTurnIds = new Set(event.turns.map((turn) => turn.id));
      const clearedPollState = clearTransientSubtitlesForTurns(state, polledTurnIds);
      return {
        ...clearedPollState,
        turns: mergeTurns(clearedPollState.turns, event.turns),
        notice: null,
      };
    }
    case "ADD_TURN":
      if (state.turns.some((turn) => turn.id === event.turn.id)) {
        return clearTransientSubtitlesForTurns(state, new Set([event.turn.id]));
      }
      const clearedTurnState = clearTransientSubtitlesForTurns(
        state,
        new Set([event.turn.id]),
      );
      return {
        ...clearedTurnState,
        phase: state.phase === "playing" ? "playing" : "active",
        turns: [...clearedTurnState.turns, event.turn],
        notice: null,
      };
    case "ADD_ASSISTANT_REPLY":
      if (state.assistantReplies.some((reply) => reply.replyId === event.reply.replyId)) {
        return state;
      }
      return {
        ...state,
        phase: state.phase === "playing" ? "playing" : "active",
        assistantReplies: [...state.assistantReplies, event.reply],
        notice: null,
      };
    case "SET_ASR_PARTIAL":
      if (state.turns.some((turn) => turn.id === event.partial.turnId)) {
        return state;
      }
      return { ...state, asrPartial: event.partial };
    case "ADD_PHRASE_SUBTITLE": {
      if (state.turns.some((turn) => turn.id === event.subtitle.utteranceId)) {
        return state;
      }
      const existing = state.phraseSubtitles.find(
        (subtitle) =>
          subtitle.utteranceId === event.subtitle.utteranceId &&
          subtitle.phraseSequence === event.subtitle.phraseSequence,
      );
      if (existing) {
        const terminal = existing.status === "translated" || existing.status === "translation_failed";
        const incomingSource = event.subtitle.status === "source_stable";
        if (terminal && incomingSource) {
          return state;
        }
        return {
          ...state,
          phraseSubtitles: state.phraseSubtitles.map((subtitle) =>
            subtitle === existing ? event.subtitle : subtitle,
          ),
        };
      }
      return {
        ...state,
        phraseSubtitles: [...state.phraseSubtitles, event.subtitle].sort(
          (left, right) =>
            left.utteranceId.localeCompare(right.utteranceId) ||
            left.phraseSequence - right.phraseSequence,
        ),
      };
    }
    case "CLEAR_ASR_PARTIAL":
      return state.asrPartial || state.phraseSubtitles.length > 0
        ? { ...state, asrPartial: null, phraseSubtitles: [] }
        : state;
    case "FALLBACK":
      return {
        ...state,
        phase: "listening",
        audioMode: "simulated",
        asrPartial: null,
        phraseSubtitles: [],
        notice: event.message,
      };
    case "ERROR":
      return {
        ...state,
        phase: state.phase === "idle" ? "idle" : "active",
        asrPartial: null,
        phraseSubtitles: [],
        notice: event.message,
      };
    case "END":
      return initialSession;
  }
}
