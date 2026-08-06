import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const getWebRTCConfig = vi.fn();
const postWebRTCOffer = vi.fn();
const postICECandidates = vi.fn();
const waitUntilRealtimeConnectionReady = vi.fn();

vi.mock("./realtime-api", () => ({
  getWebRTCConfig: (...args: unknown[]) => getWebRTCConfig(...args),
  postWebRTCOffer: (...args: unknown[]) => postWebRTCOffer(...args),
  postICECandidates: (...args: unknown[]) => postICECandidates(...args),
  waitUntilRealtimeConnectionReady: (...args: unknown[]) =>
    waitUntilRealtimeConnectionReady(...args),
}));

import { openWebRTCSession } from "./webrtc-session";

function fakeTrack(id: string) {
  return {
    id,
    kind: "audio",
    stop: vi.fn(),
    clone: vi.fn(),
  } as unknown as MediaStreamTrack;
}

class FakePeerConnection {
  connectionState: RTCPeerConnectionState = "new";
  iceConnectionState: RTCIceConnectionState = "new";
  ontrack: ((event: RTCTrackEvent) => void) | null = null;
  ondatachannel: ((event: RTCDataChannelEvent) => void) | null = null;
  onconnectionstatechange: (() => void) | null = null;
  onicecandidate: ((event: RTCPeerConnectionIceEvent) => void) | null = null;
  addTrack = vi.fn();
  createDataChannel = vi.fn(() => ({
    onmessage: null,
    close: vi.fn(),
  }));
  createOffer = vi.fn(async () => ({ type: "offer", sdp: "v=0" }));
  setLocalDescription = vi.fn(async () => undefined);
  setRemoteDescription = vi.fn(async () => undefined);
  close = vi.fn();
  addEventListener = vi.fn(
    (type: string, listener: EventListenerOrEventListenerObject) => {
      if (type === "connectionstatechange") {
        this.connectionState = "connected";
        queueMicrotask(() => {
          if (typeof listener === "function") {
            listener(new Event("connectionstatechange"));
          }
        });
      }
    },
  );
  removeEventListener = vi.fn();
}

describe("openWebRTCSession", () => {
  beforeEach(() => {
    getWebRTCConfig.mockReset();
    postWebRTCOffer.mockReset();
    postICECandidates.mockReset();
    waitUntilRealtimeConnectionReady.mockReset();
    vi.stubGlobal("RTCPeerConnection", FakePeerConnection);
    vi.stubGlobal(
      "MediaStream",
      class {
        private readonly tracks: MediaStreamTrack[];
        constructor(tracks: MediaStreamTrack[] = []) {
          this.tracks = [...tracks];
        }
        getTracks() {
          return this.tracks;
        }
        getAudioTracks() {
          return this.tracks.filter((track) => track.kind === "audio");
        }
      },
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("stops provided audio track clones when getWebRTCConfig fails", async () => {
    const track = fakeTrack("clone-1");
    getWebRTCConfig.mockRejectedValue(new Error("config failed"));

    await expect(
      openWebRTCSession({
        ticket: "ticket",
        sessionId: "vs-1",
        audioTracks: [track],
      }),
    ).rejects.toThrow("config failed");

    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(getWebRTCConfig).toHaveBeenCalledWith("ticket", "vs-1");
  });

  it("uses provided audio tracks instead of getUserMedia", async () => {
    const track = fakeTrack("clone-2");
    const getUserMedia = vi.fn();
    vi.stubGlobal("navigator", {
      mediaDevices: { getUserMedia },
    });

    getWebRTCConfig.mockResolvedValue({
      session_id: "vs-1",
      expires_at: "2099-01-01T00:00:00Z",
      ice_servers: [],
      ice_transport_policy: "all",
      data_channel: { label: "translation-events", ordered: true },
      audio: {
        uplink_codec: "opus",
        downlink_codec: "opus",
        sample_rate_hz: 48000,
        channels: 1,
      },
    });
    postWebRTCOffer.mockResolvedValue({
      sdp: "v=0",
      type: "answer",
      session_id: "vs-1",
      connection_id: "conn-1",
      data_channel_label: "translation-events",
      tts_track_id: "tts-1",
      connection_state: "connecting",
    });
    waitUntilRealtimeConnectionReady.mockResolvedValue(undefined);

    const session = await openWebRTCSession({
      ticket: "ticket",
      sessionId: "vs-1",
      audioTracks: [track],
    });

    expect(getUserMedia).not.toHaveBeenCalled();
    expect(session.connectionId).toBe("conn-1");
    expect(session.localStream.getTracks()).toEqual([track]);

    session.close();
    expect(track.stop).toHaveBeenCalledTimes(1);
  });
});
