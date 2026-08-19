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
  text: string;
  language: string;
};

export type TransientASRSubtitle = {
  turnId: string;
  text: string;
  sourceLanguage: string;
};

export type TransientPhraseSubtitle = {
  utteranceId: string;
  phraseSequence: number;
  sourceText: string;
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
    case "SET_TURNS":
      // Poll may return [] while FinalTurns only arrive on DataChannel (no API
      // outbox yet). Merge so remote never wipes locally observed subtitles.
      return {
        ...state,
        turns: mergeTurns(state.turns, event.turns),
        notice: null,
      };
    case "ADD_TURN":
      if (state.turns.some((turn) => turn.id === event.turn.id)) {
        return state.asrPartial?.turnId === event.turn.id
          ? { ...state, asrPartial: null }
          : state;
      }
      return {
        ...state,
        phase: "active",
        turns: [...state.turns, event.turn],
        asrPartial:
          state.asrPartial?.turnId === event.turn.id ? null : state.asrPartial,
        phraseSubtitles: state.phraseSubtitles.filter(
          (subtitle) => subtitle.utteranceId !== event.turn.id,
        ),
        notice: null,
      };
    case "ADD_ASSISTANT_REPLY":
      if (state.assistantReplies.some((reply) => reply.replyId === event.reply.replyId)) {
        return state;
      }
      return {
        ...state,
        phase: "active",
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
      if (existing) return state;
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
        notice: event.message,
      };
    case "ERROR":
      return {
        ...state,
        phase: state.phase === "idle" ? "idle" : "active",
        asrPartial: null,
        notice: event.message,
      };
    case "END":
      return initialSession;
  }
}
