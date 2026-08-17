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
import { WakeWordListener } from "./wake-listener";

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
    expect(sourceTrack.clone).toHaveBeenCalledTimes(1);
    expect(sourceTrack.stop).not.toHaveBeenCalled();

    listener.stop();
    expect(sourceTrack.stop).toHaveBeenCalledTimes(1);
    expect(listener.getStatus()).toBe("idle");
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
