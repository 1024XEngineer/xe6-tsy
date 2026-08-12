import { afterEach, describe, expect, it, vi } from "vitest";

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

import { WakeWordListener } from "./wake-listener";

describe("WakeWordListener", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("skips mic and model load under webdriver automation", async () => {
    const getUserMedia = vi.fn();
    vi.stubGlobal("navigator", {
      webdriver: true,
      mediaDevices: { getUserMedia },
    });

    const onStatus = vi.fn();
    const listener = new WakeWordListener({
      onCommand: vi.fn(),
      onStatus,
    });

    await listener.start();

    expect(getUserMedia).not.toHaveBeenCalled();
    expect(listener.getStatus()).toBe("error");
    expect(onStatus).toHaveBeenCalledWith("error", "自动化环境已跳过唤醒词");
  });

  it("returns empty clones when mic stream is not open", () => {
    const listener = new WakeWordListener({ onCommand: vi.fn() });
    expect(listener.cloneAudioTracksForPeer()).toEqual([]);
    expect(listener.getMediaStream()).toBeNull();
  });

  it("reports the matched assistant phrase instead of the translation label", () => {
    const onCommand = vi.fn();
    const listener = new WakeWordListener({ onCommand });

    const emitKeyword = listener as unknown as {
      emitKeyword(keyword: string): void;
    };
    emitKeyword.emitKeyword("小灵，开始对话");

    expect(onCommand).toHaveBeenCalledWith("start", "小灵，开始对话");
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

    const listener = new WakeWordListener({ onCommand: vi.fn() });
    await listener.start();

    expect(listener.getStatus()).toBe("listening");
    expect(listener.cloneAudioTracksForPeer()).toEqual([clone]);
    expect(sourceTrack.clone).toHaveBeenCalledTimes(1);
    expect(sourceTrack.stop).not.toHaveBeenCalled();

    listener.stop();
    expect(sourceTrack.stop).toHaveBeenCalledTimes(1);
    expect(listener.getStatus()).toBe("idle");
  });
});
