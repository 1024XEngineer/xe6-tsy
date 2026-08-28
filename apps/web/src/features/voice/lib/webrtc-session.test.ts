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

let dataChannelInitialState: RTCDataChannelState = "open";

class FakeDataChannel extends EventTarget {
  readyState: RTCDataChannelState;
  onmessage: ((event: MessageEvent) => void) | null = null;
  readonly close = vi.fn(() => {
    this.readyState = "closed";
  });

  constructor(readonly label: string) {
    super();
    this.readyState = dataChannelInitialState;
  }

  open(): void {
    this.readyState = "open";
    this.dispatchEvent(new Event("open"));
  }
}

class FakePeerConnection {
  static readonly instances: FakePeerConnection[] = [];
  connectionState: RTCPeerConnectionState = "new";
  iceConnectionState: RTCIceConnectionState = "new";
  ontrack: ((event: RTCTrackEvent) => void) | null = null;
  ondatachannel: ((event: RTCDataChannelEvent) => void) | null = null;
  onconnectionstatechange: (() => void) | null = null;
  onicecandidate: ((event: RTCPeerConnectionIceEvent) => void) | null = null;
  addTrack = vi.fn();
  readonly dataChannels: FakeDataChannel[] = [];
  createDataChannel = vi.fn((label: string) => {
    const channel = new FakeDataChannel(label);
    this.dataChannels.push(channel);
    return channel;
  });
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

  constructor() {
    FakePeerConnection.instances.push(this);
  }
}

function latestPeerConnection(): FakePeerConnection {
  const peerConnection = FakePeerConnection.instances.at(-1);
  if (!peerConnection) throw new Error("FakePeerConnection was not created");
  return peerConnection;
}

function mockSuccessfulSignaling(): void {
  getWebRTCConfig.mockResolvedValue({
    session_id: "vs-1",
    expires_at: "2099-01-01T00:00:00Z",
    ice_servers: [],
    ice_transport_policy: "all",
    data_channel: { label: "translation-events", ordered: true },
    control_data_channel: {
      label: "lingow-control-v1",
      ordered: true,
      protocol_version: 1,
    },
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
}

describe("openWebRTCSession", () => {
  beforeEach(() => {
    getWebRTCConfig.mockReset();
    postWebRTCOffer.mockReset();
    postICECandidates.mockReset();
    waitUntilRealtimeConnectionReady.mockReset();
    dataChannelInitialState = "open";
    FakePeerConnection.instances.length = 0;
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

    mockSuccessfulSignaling();

    const session = await openWebRTCSession({
      ticket: "ticket",
      sessionId: "vs-1",
      audioTracks: [track],
    });

    expect(getUserMedia).not.toHaveBeenCalled();
    expect(session.connectionId).toBe("conn-1");
    expect(session.localStream.getTracks()).toEqual([track]);
    expect(session.wakeWordChannel.label).toBe("translation-events");
    expect(session.controlDataChannel.label).toBe("lingow-control-v1");
    const peerConnection = latestPeerConnection();
    expect(peerConnection.createDataChannel).toHaveBeenCalledTimes(2);
    const createChannelOrder = vi.mocked(peerConnection.createDataChannel).mock
      .invocationCallOrder;
    const createOfferOrder = vi.mocked(peerConnection.createOffer).mock
      .invocationCallOrder[0]!;
    expect(createChannelOrder.every((order) => order < createOfferOrder)).toBe(
      true,
    );

    session.close();
    expect(track.stop).toHaveBeenCalledTimes(1);
  });

  it("serializes ICE candidates before the end-of-candidates marker", async () => {
    const track = fakeTrack("candidate-order");
    mockSuccessfulSignaling();

    const requests: Array<{
      candidates: RTCIceCandidate[];
      endOfCandidates: boolean;
    }> = [];
    let notifyFirstRequest!: () => void;
    let resolveFirstRequest!: () => void;
    const firstRequestStarted = new Promise<void>((resolve) => {
      notifyFirstRequest = resolve;
    });
    postICECandidates.mockImplementation(
      async (
        _ticket: string,
        _sessionId: string,
        _connectionId: string,
        candidates: RTCIceCandidate[],
        endOfCandidates: boolean,
      ) => {
        requests.push({ candidates, endOfCandidates });
        if (requests.length === 1) {
          notifyFirstRequest();
          await new Promise<void>((release) => {
            resolveFirstRequest = release;
          });
        }
        return {
          connection_id: "conn-1",
          accepted_candidate_ids: [],
          deduplicated_candidate_ids: [],
          end_of_candidates: endOfCandidates,
        };
      },
    );

    postWebRTCOffer.mockImplementation(async () => {
      const peer = latestPeerConnection();
      peer.onicecandidate?.({
        candidate: {
          candidate: "candidate:1",
          sdpMid: "0",
          sdpMLineIndex: 0,
          usernameFragment: "ufrag",
        } as RTCIceCandidate,
      } as RTCPeerConnectionIceEvent);
      peer.onicecandidate?.({ candidate: null } as RTCPeerConnectionIceEvent);
      return {
        sdp: "v=0",
        type: "answer",
        session_id: "vs-1",
        connection_id: "conn-1",
        data_channel_label: "translation-events",
        tts_track_id: "tts-1",
        connection_state: "connecting",
      };
    });

    const opening = openWebRTCSession({
      ticket: "ticket",
      sessionId: "vs-1",
      audioTracks: [track],
    });

    await firstRequestStarted;
    expect(requests).toHaveLength(1);
    expect(requests[0]?.candidates).toHaveLength(1);
    expect(requests[0]?.endOfCandidates).toBe(false);

    resolveFirstRequest();
    await vi.waitFor(() => expect(requests).toHaveLength(2));
    expect(requests[1]?.candidates).toHaveLength(0);
    expect(requests[1]?.endOfCandidates).toBe(true);

    const session = await opening;
    session.close();
  });

  it("waits until both negotiated data channels are open", async () => {
    dataChannelInitialState = "connecting";
    const track = fakeTrack("clone-3");
    mockSuccessfulSignaling();

    let settled = false;
    const opening = openWebRTCSession({
      ticket: "ticket",
      sessionId: "vs-1",
      audioTracks: [track],
    }).finally(() => {
      settled = true;
    });

    await vi.waitFor(() => {
      expect(latestPeerConnection().dataChannels).toHaveLength(2);
      expect(waitUntilRealtimeConnectionReady).toHaveBeenCalled();
    });
    latestPeerConnection().dataChannels[0]!.open();
    await Promise.resolve();
    expect(settled).toBe(false);

    latestPeerConnection().dataChannels[1]!.open();
    const session = await opening;
    expect(settled).toBe(true);
    session.close();
  });

  it("closes all WebRTC resources when a data channel fails to open", async () => {
    dataChannelInitialState = "connecting";
    const track = fakeTrack("clone-4");
    mockSuccessfulSignaling();

    const opening = openWebRTCSession({
      ticket: "ticket",
      sessionId: "vs-1",
      audioTracks: [track],
    });
    await vi.waitFor(() => {
      expect(latestPeerConnection().dataChannels).toHaveLength(2);
      expect(waitUntilRealtimeConnectionReady).toHaveBeenCalled();
    });
    latestPeerConnection().dataChannels[0]!.dispatchEvent(new Event("error"));

    await expect(opening).rejects.toThrow(
      "DataChannel translation-events 在打开前失效",
    );
    expect(track.stop).toHaveBeenCalledTimes(1);
    expect(latestPeerConnection().close).toHaveBeenCalledTimes(1);
    for (const channel of latestPeerConnection().dataChannels) {
      expect(channel.close).toHaveBeenCalledTimes(1);
    }
  });
});
