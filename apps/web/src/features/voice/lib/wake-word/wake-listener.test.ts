import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./sherpa-runtime", () => ({
  ensureSherpaKwsRuntime: vi.fn(async () => true),
  createKeywordSpotter: vi.fn(async () => ({
    createStream: () => ({}),
    isReady: () => false,
    decode: vi.fn(),
    getResult: () => ({ keyword: "" }),
    reset: vi.fn(),
    free: vi.fn(),
  })),
}));

import {
  createKeywordSpotter,
  ensureSherpaKwsRuntime,
} from "./sherpa-runtime";
import {
  PRE_ROLL_MAX_REPLAY_MS,
  WakeWordListener,
  selectWakePreRoll,
} from "./wake-listener";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
}

function fakeStream() {
  const track = {
    id: "source",
    kind: "audio",
    stop: vi.fn(),
    clone: vi.fn(),
  } as unknown as MediaStreamTrack;
  const stream = {
    getTracks: () => [track],
    getAudioTracks: () => [track],
  } as unknown as MediaStream;
  return { stream, track };
}

function fakeUplinkDestination(track = fakeStream().track) {
  return {
    stream: {
      getTracks: () => [track],
      getAudioTracks: () => [track],
    } as unknown as MediaStream,
  } as MediaStreamAudioDestinationNode;
}

describe("selectWakePreRoll", () => {
  it("starts after a sustained silence before the guarded wake-word tail", () => {
    const samples = new Float32Array(2500).fill(0.2);
    samples.fill(0, 300, 600);

    const selected = selectWakePreRoll(samples, 1000);

    expect(selected).toHaveLength(1900);
    expect(selected[0]).toBeCloseTo(0.2);
  });

  it("caps replay when no silence boundary is available", () => {
    const samples = new Float32Array(3000).fill(0.2);
    expect(selectWakePreRoll(samples, 1000)).toHaveLength(
      PRE_ROLL_MAX_REPLAY_MS,
    );
  });
});

describe("WakeWordListener", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.mocked(ensureSherpaKwsRuntime).mockResolvedValue(true);
    vi.unstubAllGlobals();
  });

  it("returns empty clones when mic stream is not open", () => {
    const listener = new WakeWordListener({ onWake: vi.fn() });
    expect(listener.cloneAudioTracksForPeer()).toEqual([]);
    expect(listener.getMediaStream()).toBeNull();
  });

  it("reports only the canonical fixed wake phrase", () => {
    const onWake = vi.fn();
    const listener = new WakeWordListener({ onWake });

    const emitKeyword = listener as unknown as {
      emitKeyword(keyword: string): void;
    };
    emitKeyword.emitKeyword("小灵小灵");
    emitKeyword.emitKeyword("小林小林");

    expect(onWake).toHaveBeenCalledOnce();
    expect(onWake).toHaveBeenCalledWith("小灵小灵");
  });

  it("clones open mic tracks for WebRTC without stopping the source", async () => {
    const clone = {
      id: "clone",
      kind: "audio",
      stop: vi.fn(),
    } as unknown as MediaStreamTrack;
    const sourceTrack = {
      id: "source",
      kind: "audio",
      stop: vi.fn(),
      clone: vi.fn(() => clone),
    } as unknown as MediaStreamTrack;
    const uplinkTrack = {
      id: "uplink",
      kind: "audio",
      stop: vi.fn(),
      clone: vi.fn(() => clone),
    } as unknown as MediaStreamTrack;
    const stream = {
      getTracks: () => [sourceTrack],
      getAudioTracks: () => [sourceTrack],
    } as unknown as MediaStream;

    vi.stubGlobal("navigator", {
      webdriver: false,
      mediaDevices: {
        getUserMedia: vi.fn(async () => stream),
      },
    });

    class FakeAudioContext {
      sampleRate = 16000;
      state: AudioContextState = "running";
      destination = {} as AudioDestinationNode;
      createMediaStreamSource = vi.fn(() => ({ connect: vi.fn() }));
      createMediaStreamDestination = vi.fn(() =>
        fakeUplinkDestination(uplinkTrack),
      );
      createScriptProcessor = vi.fn(() => ({
        onaudioprocess: null,
        connect: vi.fn(),
        disconnect: vi.fn(),
      }));
      createGain = vi.fn(() => ({
        gain: { value: 1 },
        connect: vi.fn(),
      }));
      resume = vi.fn(async () => undefined);
      close = vi.fn(async () => undefined);
    }
    vi.stubGlobal("AudioContext", FakeAudioContext);

    const listener = new WakeWordListener({ onWake: vi.fn() });
    await listener.start();

    expect(listener.getStatus()).toBe("listening");
    expect(listener.cloneAudioTracksForPeer()).toEqual([clone]);
    expect(uplinkTrack.clone).toHaveBeenCalledTimes(1);
    expect(sourceTrack.stop).not.toHaveBeenCalled();

    listener.stop();
    expect(sourceTrack.stop).toHaveBeenCalledTimes(1);
    expect(uplinkTrack.stop).toHaveBeenCalledTimes(1);
    expect(listener.getStatus()).toBe("idle");
  });

  it("replays buffered wake audio, then supports closed and continuous output", () => {
    const listener = new WakeWordListener({ onWake: vi.fn() });
    const harness = listener as unknown as {
      recordSampleRate: number;
      audioFrame: number;
      appendPreRoll(input: Float32Array): void;
      writeUplinkFrame(
        input: Float32Array,
        output: Float32Array,
        frame: number,
      ): void;
    };
    harness.recordSampleRate = 1000;
    harness.audioFrame = 1;
    const buffered = new Float32Array(2500).fill(0.25);
    buffered.fill(0, 300, 600);
    harness.appendPreRoll(buffered);

    listener.openCommandUplink();
    const replay = new Float32Array(4);
    harness.writeUplinkFrame(new Float32Array([0.8, 0.8, 0.8, 0.8]), replay, 1);
    expect([...replay]).toEqual([0.25, 0.25, 0.25, 0.25]);

    listener.setUplinkEnabled(false);
    const closed = new Float32Array(4).fill(1);
    harness.writeUplinkFrame(new Float32Array(4).fill(0.8), closed, 2);
    expect([...closed]).toEqual([0, 0, 0, 0]);

    listener.setUplinkEnabled(true);
    const continuous = new Float32Array(4);
    harness.writeUplinkFrame(
      new Float32Array([0.1, 0.2, 0.3, 0.4]),
      continuous,
      3,
    );
    expect([...continuous]).toEqual([
      expect.closeTo(0.1),
      expect.closeTo(0.2),
      expect.closeTo(0.3),
      expect.closeTo(0.4),
    ]);
  });

  it("preserves an active replay while TTS output is suppressed", () => {
    const listener = new WakeWordListener({ onWake: vi.fn() });
    const harness = listener as unknown as {
      recordSampleRate: number;
      audioFrame: number;
      replayChunks: Float32Array[];
      appendPreRoll(input: Float32Array): void;
      writeUplinkFrame(
        input: Float32Array,
        output: Float32Array,
        frame: number,
      ): void;
    };
    harness.recordSampleRate = 1000;
    harness.audioFrame = 1;
    harness.appendPreRoll(new Float32Array(2500).fill(0.25));

    listener.openCommandUplink();
    harness.replayChunks = [new Float32Array([0.25, 0.25])];
    listener.setOutputSuppressed(true);
    const suppressed = new Float32Array(4).fill(1);
    harness.writeUplinkFrame(new Float32Array(4).fill(0.8), suppressed, 2);
    expect([...suppressed]).toEqual([0, 0, 0, 0]);

    listener.setOutputSuppressed(false);
    const resumed = new Float32Array(4);
    harness.writeUplinkFrame(new Float32Array(4).fill(0.9), resumed, 3);
    expect([...resumed]).toEqual([
      expect.closeTo(0.25),
      expect.closeTo(0.25),
      expect.closeTo(0.8),
      expect.closeTo(0.8),
    ]);
  });

  it("initializes KWS when the browser exposes webdriver", async () => {
    const { stream } = fakeStream();
    const getUserMedia = vi.fn(async () => stream);
    vi.stubGlobal("navigator", {
      webdriver: true,
      mediaDevices: { getUserMedia },
    });

    class FakeAudioContext {
      sampleRate = 16000;
      state: AudioContextState = "running";
      destination = {} as AudioDestinationNode;
      createMediaStreamSource = vi.fn(() => ({
        connect: vi.fn(),
        disconnect: vi.fn(),
      }));
      createMediaStreamDestination = vi.fn(() => fakeUplinkDestination());
      createScriptProcessor = vi.fn(() => ({
        onaudioprocess: null,
        connect: vi.fn(),
        disconnect: vi.fn(),
      }));
      createGain = vi.fn(() => ({ gain: { value: 1 }, connect: vi.fn() }));
      resume = vi.fn(async () => undefined);
      close = vi.fn(async () => undefined);
    }
    vi.stubGlobal("AudioContext", FakeAudioContext);

    const listener = new WakeWordListener({ onWake: vi.fn() });
    await listener.start();

    expect(getUserMedia).toHaveBeenCalledTimes(1);
    expect(ensureSherpaKwsRuntime).toHaveBeenCalledTimes(1);
    expect(createKeywordSpotter).toHaveBeenCalledTimes(1);
    expect(listener.getStatus()).toBe("listening");
    listener.stop();
  });

  it("releases a microphone granted after stop and stays idle", async () => {
    const mic = deferred<MediaStream>();
    const { stream, track } = fakeStream();
    vi.stubGlobal("navigator", {
      webdriver: false,
      mediaDevices: { getUserMedia: vi.fn(() => mic.promise) },
    });

    const listener = new WakeWordListener({ onWake: vi.fn() });
    const starting = listener.start();
    listener.stop();
    mic.resolve(stream);
    await starting;

    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(listener.getMediaStream()).toBeNull();
    expect(listener.getStatus()).toBe("idle");
    expect(createKeywordSpotter).not.toHaveBeenCalled();
  });

  it("does not finish model initialization after stop", async () => {
    const runtime = deferred<boolean>();
    const { stream, track } = fakeStream();
    vi.mocked(ensureSherpaKwsRuntime).mockReturnValueOnce(runtime.promise);
    vi.stubGlobal("navigator", {
      webdriver: false,
      mediaDevices: { getUserMedia: vi.fn(async () => stream) },
    });

    const listener = new WakeWordListener({ onWake: vi.fn() });
    const starting = listener.start();
    await vi.waitFor(() => expect(listener.getStatus()).toBe("loading_model"));
    listener.stop();
    runtime.resolve(true);
    await starting;

    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(listener.getMediaStream()).toBeNull();
    expect(listener.getStatus()).toBe("idle");
    expect(createKeywordSpotter).not.toHaveBeenCalled();
  });
});
