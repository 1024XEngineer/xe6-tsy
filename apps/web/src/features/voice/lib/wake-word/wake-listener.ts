/**
 * Session-scoped microphone capture feeding sherpa-onnx keyword spotting.
 * Owns a MediaStream that can be cloned for WebRTC uplink.
 */

import { resolveWakePhrase } from "./keywords";
import {
  createKeywordSpotter,
  ensureSherpaKwsRuntime,
  type SherpaKwsSpotter,
  type SherpaKwsStream,
} from "./sherpa-runtime";

const TARGET_SAMPLE_RATE = 16000;
const PROCESSOR_BUFFER_SIZE = 2048;
const COOLDOWN_MS = 1800;
export const PRE_ROLL_CAPACITY_MS = 2500;
export const PRE_ROLL_MAX_REPLAY_MS = 2000;
const SILENCE_BOUNDARY_MS = 160;
const WAKE_TAIL_GUARD_MS = 900;
const SILENCE_RMS_THRESHOLD = 0.012;

type UplinkMode = "closed" | "continuous" | "replay";

export type WakeListenerStatus =
  | "idle"
  | "requesting_mic"
  | "loading_model"
  | "listening"
  | "error";

export type WakeListenerHandlers = {
  /** Second arg is the exact catalog phrase matched in the KWS result. */
  onWake: (keyword: string) => void;
  onStatus?: (status: WakeListenerStatus, detail?: string) => void;
};

function downsampleBuffer(
  buffer: Float32Array,
  recordSampleRate: number,
  exportSampleRate: number,
): Float32Array {
  if (exportSampleRate === recordSampleRate) {
    return buffer;
  }
  const sampleRateRatio = recordSampleRate / exportSampleRate;
  const newLength = Math.round(buffer.length / sampleRateRatio);
  const result = new Float32Array(newLength);
  let offsetResult = 0;
  let offsetBuffer = 0;
  while (offsetResult < result.length) {
    const nextOffsetBuffer = Math.round((offsetResult + 1) * sampleRateRatio);
    let accum = 0;
    let count = 0;
    for (
      let i = offsetBuffer;
      i < nextOffsetBuffer && i < buffer.length;
      i += 1
    ) {
      accum += buffer[i]!;
      count += 1;
    }
    result[offsetResult] = count > 0 ? accum / count : 0;
    offsetResult += 1;
    offsetBuffer = nextOffsetBuffer;
  }
  return result;
}

/** Select the utterance start before the wake phrase, capped to two seconds. */
export function selectWakePreRoll(
  samples: Float32Array,
  sampleRate: number,
): Float32Array {
  if (samples.length === 0 || sampleRate <= 0) return new Float32Array();
  const maxReplay = Math.round((sampleRate * PRE_ROLL_MAX_REPLAY_MS) / 1000);
  const silenceSize = Math.max(
    1,
    Math.round((sampleRate * SILENCE_BOUNDARY_MS) / 1000),
  );
  const guardedTail = Math.round((sampleRate * WAKE_TAIL_GUARD_MS) / 1000);
  let boundary = Math.max(0, samples.length - maxReplay);

  for (
    let end = samples.length - guardedTail;
    end >= silenceSize;
    end -= Math.max(1, Math.floor(silenceSize / 4))
  ) {
    let energy = 0;
    for (let index = end - silenceSize; index < end; index += 1) {
      energy += samples[index]! * samples[index]!;
    }
    if (Math.sqrt(energy / silenceSize) <= SILENCE_RMS_THRESHOLD) {
      boundary = end;
      break;
    }
  }

  return new Float32Array(
    samples.subarray(Math.max(boundary, samples.length - maxReplay)),
  );
}

export class WakeWordListener {
  private readonly handlers: WakeListenerHandlers;
  private stream: MediaStream | null = null;
  private audioCtx: AudioContext | null = null;
  private source: MediaStreamAudioSourceNode | null = null;
  private processor: ScriptProcessorNode | null = null;
  private uplinkDestination: MediaStreamAudioDestinationNode | null = null;
  private spotter: SherpaKwsSpotter | null = null;
  private kwsStream: SherpaKwsStream | null = null;
  private recordSampleRate = TARGET_SAMPLE_RATE;
  private preRollChunks: Float32Array[] = [];
  private preRollSampleCount = 0;
  private uplinkMode: UplinkMode = "closed";
  private replayChunks: Float32Array[] = [];
  private replayOffset = 0;
  private audioFrame = 0;
  private replayOpenedFrame = -1;
  private running = false;
  private startGeneration = 0;
  private lastFireAt = 0;
  private status: WakeListenerStatus = "idle";

  constructor(handlers: WakeListenerHandlers) {
    this.handlers = handlers;
  }

  getMediaStream(): MediaStream | null {
    return this.stream;
  }

  getStatus(): WakeListenerStatus {
    return this.status;
  }

  private setStatus(status: WakeListenerStatus, detail?: string): void {
    this.status = status;
    this.handlers.onStatus?.(status, detail);
  }

  async start(): Promise<void> {
    if (this.running) return;
    this.running = true;
    const generation = ++this.startGeneration;
    this.lastFireAt = 0;

    try {
      this.setStatus("requesting_mic");
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
        },
        video: false,
      });
      if (!this.isActiveStart(generation)) {
        for (const track of stream.getTracks()) track.stop();
        return;
      }
      this.stream = stream;

      this.setStatus("loading_model", "正在加载唤醒模型…");
      const ready = await ensureSherpaKwsRuntime();
      if (!this.isActiveStart(generation)) return;
      if (!ready) {
        throw new Error("sherpa-onnx WASM 加载失败");
      }
      const spotter = await createKeywordSpotter();
      if (!this.isActiveStart(generation)) {
        try {
          spotter.free();
        } catch {
          // ignore
        }
        return;
      }
      this.spotter = spotter;
      this.kwsStream = this.spotter.createStream();

      this.audioCtx = new AudioContext({ sampleRate: TARGET_SAMPLE_RATE });
      // Some browsers ignore the requested rate; downsample if needed.
      const recordRate = this.audioCtx.sampleRate;
      this.recordSampleRate = recordRate;
      this.source = this.audioCtx.createMediaStreamSource(this.stream);
      this.uplinkDestination = this.audioCtx.createMediaStreamDestination();
      this.processor = this.audioCtx.createScriptProcessor(
        PROCESSOR_BUFFER_SIZE,
        1,
        1,
      );
      this.processor.onaudioprocess = (event) => {
        if (!this.running || !this.spotter || !this.kwsStream) return;
        const frame = ++this.audioFrame;
        const input = new Float32Array(event.inputBuffer.getChannelData(0));
        this.appendPreRoll(input);
        const samples = downsampleBuffer(
          input,
          recordRate,
          TARGET_SAMPLE_RATE,
        );
        this.kwsStream.acceptWaveform(TARGET_SAMPLE_RATE, samples);
        while (this.spotter.isReady(this.kwsStream)) {
          this.spotter.decode(this.kwsStream);
          const result = this.spotter.getResult(this.kwsStream);
          const keyword = result.keyword?.trim() ?? "";
          if (!keyword) continue;
          this.spotter.reset(this.kwsStream);
          this.emitKeyword(keyword);
        }
        this.writeUplinkFrame(
          input,
          event.outputBuffer.getChannelData(0),
          frame,
        );
      };

      this.source.connect(this.processor);
      this.processor.connect(this.uplinkDestination);
      // ScriptProcessor must connect to destination to run, but keep silent.
      const mute = this.audioCtx.createGain();
      mute.gain.value = 0;
      this.processor.connect(mute);
      mute.connect(this.audioCtx.destination);

      if (this.audioCtx.state === "suspended") {
        await this.audioCtx.resume();
      }
      if (!this.isActiveStart(generation)) return;

      this.setStatus("listening");
    } catch (error) {
      // stop() invalidates in-flight starts. Their late completion must not
      // resurrect the microphone or overwrite the idle UI with an error.
      if (!this.isActiveStart(generation)) return;
      this.running = false;
      const message =
        error instanceof Error ? error.message : "唤醒词监听启动失败";
      this.releaseResources();
      this.setStatus("error", message);
      throw error;
    }
  }

  private isActiveStart(generation: number): boolean {
    return this.running && this.startGeneration === generation;
  }

  private emitKeyword(keyword: string): void {
    const now = Date.now();
    const match = resolveWakePhrase(keyword);
    if (!match) return;
    if (now - this.lastFireAt < COOLDOWN_MS) {
      return;
    }
    this.lastFireAt = now;
    this.handlers.onWake(match.phrase);
  }

  setUplinkEnabled(enabled: boolean): void {
    this.uplinkMode = enabled ? "continuous" : "closed";
    this.clearReplay();
  }

  /** Start one delayed uplink turn with the buffered wake phrase included. */
  openCommandUplink(): void {
    const preRoll = selectWakePreRoll(
      this.snapshotPreRoll(),
      this.recordSampleRate,
    );
    this.uplinkMode = "replay";
    this.replayChunks = preRoll.length > 0 ? [preRoll] : [];
    this.replayOffset = 0;
    this.replayOpenedFrame = this.audioFrame;
  }

  /** Clone the audio bridge track so KWS capture remains session-owned. */
  cloneAudioTracksForPeer(): MediaStreamTrack[] {
    if (!this.uplinkDestination) return [];
    return this.uplinkDestination.stream
      .getAudioTracks()
      .map((track) => track.clone());
  }

  private appendPreRoll(input: Float32Array): void {
    this.preRollChunks.push(input);
    this.preRollSampleCount += input.length;
    const capacity = Math.round(
      (this.recordSampleRate * PRE_ROLL_CAPACITY_MS) / 1000,
    );
    while (
      this.preRollChunks[0] &&
      this.preRollSampleCount - this.preRollChunks[0].length >= capacity
    ) {
      this.preRollSampleCount -= this.preRollChunks.shift()!.length;
    }
    if (this.preRollSampleCount > capacity && this.preRollChunks[0]) {
      const excess = this.preRollSampleCount - capacity;
      this.preRollChunks[0] = this.preRollChunks[0].slice(excess);
      this.preRollSampleCount = capacity;
    }
  }

  private snapshotPreRoll(): Float32Array {
    const snapshot = new Float32Array(this.preRollSampleCount);
    let offset = 0;
    for (const chunk of this.preRollChunks) {
      snapshot.set(chunk, offset);
      offset += chunk.length;
    }
    return snapshot;
  }

  private writeUplinkFrame(
    input: Float32Array,
    output: Float32Array,
    frame: number,
  ): void {
    output.fill(0);
    if (this.uplinkMode === "continuous") {
      output.set(input.subarray(0, output.length));
      return;
    }
    if (this.uplinkMode !== "replay") return;
    if (this.replayOpenedFrame !== frame) this.replayChunks.push(input);

    let written = 0;
    while (written < output.length && this.replayChunks[0]) {
      const chunk = this.replayChunks[0];
      const count = Math.min(
        output.length - written,
        chunk.length - this.replayOffset,
      );
      output.set(
        chunk.subarray(this.replayOffset, this.replayOffset + count),
        written,
      );
      written += count;
      this.replayOffset += count;
      if (this.replayOffset === chunk.length) {
        this.replayChunks.shift();
        this.replayOffset = 0;
      }
    }
  }

  private clearReplay(): void {
    this.replayChunks = [];
    this.replayOffset = 0;
    this.replayOpenedFrame = -1;
  }

  stop(): void {
    this.running = false;
    this.startGeneration += 1;
    this.releaseResources();
    this.setStatus("idle");
  }

  private releaseResources(): void {
    this.uplinkMode = "closed";
    this.clearReplay();
    this.preRollChunks = [];
    this.preRollSampleCount = 0;
    this.stopMicGraph();
    if (this.kwsStream) {
      try {
        this.kwsStream.free();
      } catch {
        // ignore
      }
      this.kwsStream = null;
    }
    if (this.spotter) {
      try {
        this.spotter.free();
      } catch {
        // ignore
      }
      this.spotter = null;
    }
    if (this.stream) {
      for (const track of this.stream.getTracks()) {
        track.stop();
      }
      this.stream = null;
    }
  }

  private stopMicGraph(): void {
    try {
      this.processor?.disconnect();
    } catch {
      // ignore
    }
    try {
      this.source?.disconnect();
    } catch {
      // ignore
    }
    this.processor = null;
    this.source = null;
    if (this.uplinkDestination) {
      for (const track of this.uplinkDestination.stream.getTracks()) {
        track.stop();
      }
      this.uplinkDestination = null;
    }
    if (this.audioCtx) {
      void this.audioCtx.close().catch(() => undefined);
      this.audioCtx = null;
    }
  }
}
