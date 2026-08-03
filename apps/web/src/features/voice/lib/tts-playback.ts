/** Play TTS audio delivered over the realtime DataChannel (possibly chunked). */

export type TTSAudioEvent = {
  playbackId: string;
  sampleRateHz: number;
  channels: number;
  encoding: string;
  sequence: number;
  final: boolean;
  pcm: ArrayBuffer;
};

export type TTSAudioPlaybackListener = (playing: boolean) => void;

type PendingPlayback = {
  sampleRateHz: number;
  channels: number;
  encoding: string;
  chunks: Map<number, ArrayBuffer>;
  listener?: TTSAudioPlaybackListener;
};

let sharedContext: AudioContext | null = null;
let playChain: Promise<void> = Promise.resolve();
const pendingByPlayback = new Map<string, PendingPlayback>();

function getAudioContext(sampleRateHz: number): AudioContext {
  if (!sharedContext || sharedContext.state === "closed") {
    sharedContext = new AudioContext({ sampleRate: sampleRateHz });
  }
  return sharedContext;
}

function pcmS16leToAudioBuffer(
  ctx: AudioContext,
  pcm: ArrayBuffer,
  sampleRateHz: number,
  channels: number,
): AudioBuffer {
  const bytes = new Uint8Array(pcm);
  const frameCount = Math.floor(bytes.byteLength / (2 * channels));
  const buffer = ctx.createBuffer(channels, Math.max(frameCount, 1), sampleRateHz);
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  for (let ch = 0; ch < channels; ch += 1) {
    const channel = buffer.getChannelData(ch);
    for (let i = 0; i < frameCount; i += 1) {
      channel[i] = view.getInt16((i * channels + ch) * 2, true) / 32768;
    }
  }
  return buffer;
}

function looksLikeContainerAudio(pcm: ArrayBuffer): boolean {
  if (pcm.byteLength < 12) return false;
  const head = new Uint8Array(pcm, 0, 12);
  const riff =
    head[0] === 0x52 && head[1] === 0x49 && head[2] === 0x46 && head[3] === 0x46;
  const id3 = head[0] === 0x49 && head[1] === 0x44 && head[2] === 0x33;
  const mpeg = head[0] === 0xff && (head[1] & 0xe0) === 0xe0;
  return riff || id3 || mpeg;
}

function concatChunks(chunks: Map<number, ArrayBuffer>): ArrayBuffer {
  const sequences = [...chunks.keys()].sort((a, b) => a - b);
  let total = 0;
  for (const seq of sequences) {
    total += chunks.get(seq)?.byteLength ?? 0;
  }
  const out = new Uint8Array(total);
  let offset = 0;
  for (const seq of sequences) {
    const part = new Uint8Array(chunks.get(seq)!);
    out.set(part, offset);
    offset += part.byteLength;
  }
  return out.buffer;
}

async function toAudioBuffer(
  ctx: AudioContext,
  event: Omit<TTSAudioEvent, "sequence" | "final" | "playbackId"> & {
    pcm: ArrayBuffer;
  },
): Promise<AudioBuffer> {
  const encoding = event.encoding || "";
  if (encoding === "pcm_s16le") {
    return pcmS16leToAudioBuffer(
      ctx,
      event.pcm,
      event.sampleRateHz,
      Math.max(1, event.channels || 1),
    );
  }
  if (encoding === "audio_container" || looksLikeContainerAudio(event.pcm)) {
    const copy = event.pcm.slice(0);
    return ctx.decodeAudioData(copy);
  }
  return pcmS16leToAudioBuffer(
    ctx,
    event.pcm,
    event.sampleRateHz,
    Math.max(1, event.channels || 1),
  );
}

async function playAssembled(event: {
  sampleRateHz: number;
  channels: number;
  encoding: string;
  pcm: ArrayBuffer;
}, listener?: TTSAudioPlaybackListener): Promise<void> {
  listener?.(true);
  try {
    const ctx = getAudioContext(event.sampleRateHz);
    if (ctx.state === "suspended") {
      try {
        await ctx.resume();
      } catch {
        return;
      }
      if (ctx.state === "suspended") {
        return;
      }
    }
    const audioBuffer = await toAudioBuffer(ctx, event);
    await new Promise<void>((resolve, reject) => {
      const source = ctx.createBufferSource();
      source.buffer = audioBuffer;
      source.connect(ctx.destination);
      source.onended = () => resolve();
      try {
        source.start();
      } catch (error) {
        reject(error);
      }
    });
  } finally {
    listener?.(false);
  }
}

/** Queue TTS clips so overlapping turns do not stomp each other. */
export function enqueueTTSAudio(
  event: TTSAudioEvent,
  listener?: TTSAudioPlaybackListener,
): void {
  const playbackId = event.playbackId || "default";
  let pending = pendingByPlayback.get(playbackId);
  if (!pending) {
    pending = {
      sampleRateHz: event.sampleRateHz,
      channels: event.channels,
      encoding: event.encoding,
      chunks: new Map(),
      listener,
    };
    pendingByPlayback.set(playbackId, pending);
  } else if (!pending.listener && listener) {
    pending.listener = listener;
  }
  pending.chunks.set(event.sequence || pending.chunks.size + 1, event.pcm);
  if (event.encoding) {
    pending.encoding = event.encoding;
  }
  if (!event.final) {
    return;
  }
  pendingByPlayback.delete(playbackId);
  const assembled = {
    sampleRateHz: pending.sampleRateHz,
    channels: pending.channels,
    encoding: pending.encoding,
    pcm: concatChunks(pending.chunks),
  };
  playChain = playChain
    .catch(() => undefined)
    .then(() => playAssembled(assembled, pending.listener));
}

export function parseTTSAudioEvent(payload: unknown): TTSAudioEvent | null {
  if (!payload || typeof payload !== "object") return null;
  const root = payload as Record<string, unknown>;
  const type = String(root.type ?? root.event ?? "");
  if (type !== "tts.audio") return null;
  const nested =
    root.payload && typeof root.payload === "object"
      ? (root.payload as Record<string, unknown>)
      : root;
  const pcmBase64 = String(nested.pcm_base64 ?? nested.pcmBase64 ?? "");
  const sampleRateHz = Number(nested.sample_rate_hz ?? nested.sampleRateHz ?? 0);
  if (!pcmBase64 || !Number.isFinite(sampleRateHz) || sampleRateHz <= 0) {
    return null;
  }
  try {
    const binary = atob(pcmBase64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i += 1) {
      bytes[i] = binary.charCodeAt(i);
    }
    const hasFinalField = Object.prototype.hasOwnProperty.call(nested, "final");
    const finalRaw = nested.final;
    const final =
      finalRaw === true ||
      finalRaw === 1 ||
      finalRaw === "true" ||
      // Backward compat: older servers omit `final` and send one full clip.
      !hasFinalField;
    return {
      playbackId: String(nested.playback_id ?? nested.playbackId ?? ""),
      sampleRateHz,
      channels: Number(nested.channels ?? 1) || 1,
      encoding: String(nested.encoding ?? ""),
      sequence: Number(nested.sequence ?? 1) || 1,
      final,
      pcm: bytes.buffer,
    };
  } catch {
    return null;
  }
}
