/**
 * Session-scoped microphone capture feeding sherpa-onnx keyword spotting.
 * Owns a MediaStream that can be cloned for WebRTC uplink.
 */

import {
  resolveWakePhrase,
} from "./keywords";
import {
  createKeywordSpotter,
  ensureSherpaKwsRuntime,
  type SherpaKwsSpotter,
  type SherpaKwsStream,
} from "./sherpa-runtime";

const TARGET_SAMPLE_RATE = 16000;
const COOLDOWN_MS = 1800;

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

export class WakeWordListener {
  private readonly handlers: WakeListenerHandlers;
  private stream: MediaStream | null = null;
  private audioCtx: AudioContext | null = null;
  private source: MediaStreamAudioSourceNode | null = null;
  private processor: ScriptProcessorNode | null = null;
  private spotter: SherpaKwsSpotter | null = null;
  private kwsStream: SherpaKwsStream | null = null;
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
      this.source = this.audioCtx.createMediaStreamSource(this.stream);
      const bufferSize = 4096;
      this.processor = this.audioCtx.createScriptProcessor(bufferSize, 1, 1);
      this.processor.onaudioprocess = (event) => {
        if (!this.running || !this.spotter || !this.kwsStream) return;
        const input = event.inputBuffer.getChannelData(0);
        const samples = downsampleBuffer(
          new Float32Array(input),
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
      };

      this.source.connect(this.processor);
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

  /** Clone mic tracks for WebRTC so TTS mute / session close won't stop KWS. */
  cloneAudioTracksForPeer(): MediaStreamTrack[] {
    if (!this.stream) return [];
    return this.stream.getAudioTracks().map((track) => track.clone());
  }

  stop(): void {
    this.running = false;
    this.startGeneration += 1;
    this.releaseResources();
    this.setStatus("idle");
  }

  private releaseResources(): void {
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
    if (this.audioCtx) {
      void this.audioCtx.close().catch(() => undefined);
      this.audioCtx = null;
    }
  }
}
