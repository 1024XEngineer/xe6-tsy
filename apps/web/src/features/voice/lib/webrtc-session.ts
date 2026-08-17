import {
  getWebRTCConfig,
  postICECandidates,
  postWebRTCOffer,
  waitUntilRealtimeConnectionReady,
  type OfferResponse,
} from "./realtime-api";

export type WebRTCSessionHandles = {
  connectionId: string;
  peerConnection: RTCPeerConnection;
  localStream: MediaStream;
  remoteAudio: HTMLAudioElement;
  wakeWordChannel: RTCDataChannel;
  controlDataChannel: RTCDataChannel;
  close: () => void;
};

export type WebRTCSessionOptions = {
  ticket: string;
  sessionId: string;
  onDataMessage?: (payload: unknown) => void;
  onConnectionStateChange?: (state: RTCPeerConnectionState) => void;
  /**
   * Optional mic tracks already opened (e.g. wake-word listener clones).
   * Ownership transfers to this function immediately: close() and failure
   * cleanup always stop these tracks. Callers must pass clones so the
   * session-scoped wake stream stays alive until the session ends.
   */
  audioTracks?: MediaStreamTrack[];
};

function toIceServers(
  servers: Array<{ urls: string[]; username?: string; credential?: string }>,
): RTCIceServer[] {
  return servers.map((server) => ({
    urls: server.urls,
    username: server.username,
    credential: server.credential,
  }));
}

function waitForPeerConnectionConnected(
  peerConnection: RTCPeerConnection,
  timeoutMs: number,
): Promise<void> {
  if (peerConnection.connectionState === "connected") {
    return Promise.resolve();
  }
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => {
      cleanup();
      reject(
        new Error(
          `浏览器 PeerConnection 未在 ${timeoutMs}ms 内 connected（当前=${peerConnection.connectionState}, ice=${peerConnection.iceConnectionState}）`,
        ),
      );
    }, timeoutMs);

    const onState = () => {
      const state = peerConnection.connectionState;
      if (state === "connected") {
        cleanup();
        resolve();
        return;
      }
      if (state === "failed" || state === "closed" || state === "disconnected") {
        cleanup();
        reject(
          new Error(
            `浏览器 PeerConnection 异常：${state}（ice=${peerConnection.iceConnectionState}）`,
          ),
        );
      }
    };

    const cleanup = () => {
      window.clearTimeout(timer);
      peerConnection.removeEventListener("connectionstatechange", onState);
    };

    peerConnection.addEventListener("connectionstatechange", onState);
    onState();
  });
}

function waitForDataChannelOpen(
  channel: RTCDataChannel,
  timeoutMs: number,
): Promise<void> {
  if (channel.readyState === "open") return Promise.resolve();
  if (channel.readyState === "closing" || channel.readyState === "closed") {
    return Promise.reject(
      new Error(`DataChannel ${channel.label} 已关闭，无法启动语音命令入口`),
    );
  }
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => {
      cleanup();
      reject(
        new Error(
          `DataChannel ${channel.label} 未在 ${timeoutMs}ms 内打开（当前=${channel.readyState}）`,
        ),
      );
    }, timeoutMs);
    const onOpen = () => {
      cleanup();
      resolve();
    };
    const onFailure = () => {
      cleanup();
      reject(new Error(`DataChannel ${channel.label} 在打开前失效`));
    };
    const cleanup = () => {
      window.clearTimeout(timer);
      channel.removeEventListener("open", onOpen);
      channel.removeEventListener("close", onFailure);
      channel.removeEventListener("error", onFailure);
    };
    channel.addEventListener("open", onOpen);
    channel.addEventListener("close", onFailure);
    channel.addEventListener("error", onFailure);
  });
}

function closePartialSession(parts: {
  peerConnection?: RTCPeerConnection | null;
  localStream?: MediaStream | null;
  remoteAudio?: HTMLAudioElement | null;
  wakeWordChannel?: RTCDataChannel | null;
  controlDataChannel?: RTCDataChannel | null;
}): void {
  try {
    parts.wakeWordChannel?.close();
  } catch {
    // ignore
  }
  try {
    parts.controlDataChannel?.close();
  } catch {
    // ignore
  }
  if (parts.localStream) {
    for (const track of parts.localStream.getTracks()) {
      track.stop();
    }
  }
  try {
    parts.peerConnection?.close();
  } catch {
    // ignore
  }
  if (parts.remoteAudio) {
    parts.remoteAudio.srcObject = null;
    parts.remoteAudio.remove();
  }
}

export async function openWebRTCSession(
  options: WebRTCSessionOptions,
): Promise<WebRTCSessionHandles> {
  let peerConnection: RTCPeerConnection | null = null;
  let localStream: MediaStream | null = null;
  let remoteAudio: HTMLAudioElement | null = null;
  let wakeWordChannel: RTCDataChannel | null = null;
  let controlDataChannel: RTCDataChannel | null = null;

  // Claim provided clones before any await so getWebRTCConfig / later failures
  // still run closePartialSession and stop leaked mic tracks.
  if (options.audioTracks && options.audioTracks.length > 0) {
    localStream = new MediaStream(options.audioTracks);
  }

  try {
    const config = await getWebRTCConfig(options.ticket, options.sessionId);

    peerConnection = new RTCPeerConnection({
      iceServers: toIceServers(config.ice_servers),
      iceTransportPolicy:
        config.ice_transport_policy === "relay" ? "relay" : "all",
    });

    if (!localStream) {
      localStream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
          channelCount: 1,
          sampleRate: 16_000,
        },
        video: false,
      });
    }

    for (const track of localStream.getTracks()) {
      peerConnection.addTrack(track, localStream);
    }

    remoteAudio = document.createElement("audio");
    remoteAudio.autoplay = true;
    remoteAudio.setAttribute("playsinline", "true");

    peerConnection.ontrack = (event) => {
      const [stream] = event.streams;
      if (stream && remoteAudio) {
        remoteAudio.srcObject = stream;
        void remoteAudio.play().catch(() => {
          // Autoplay may be blocked until a user gesture; the click that started
          // the session usually counts, but ignore failures here.
        });
      }
    };

    const eventLabel = config.data_channel.label || "translation-events";
    const controlLabel =
      config.control_data_channel?.label || "lingow-control-v1";
    const bindDataChannel = (channel: RTCDataChannel) => {
      channel.onmessage = (event) => {
        if (!options.onDataMessage) return;
        try {
          options.onDataMessage(JSON.parse(String(event.data)));
        } catch {
          options.onDataMessage(event.data);
        }
      };
    };
    // Realtime receives wake_word.detected on the event label and typed mode
    // controls on a separate protocol channel. Both must exist in the Offer.
    wakeWordChannel = peerConnection.createDataChannel(eventLabel, {
      ordered: config.data_channel.ordered,
    });
    controlDataChannel = peerConnection.createDataChannel(controlLabel, {
      ordered: config.control_data_channel?.ordered ?? true,
    });
    bindDataChannel(wakeWordChannel);
    bindDataChannel(controlDataChannel);
    peerConnection.ondatachannel = (event) => {
      if (
        event.channel.label === eventLabel ||
        event.channel.label === controlLabel
      ) {
        bindDataChannel(event.channel);
      }
    };

    peerConnection.onconnectionstatechange = () => {
      if (peerConnection) {
        options.onConnectionStateChange?.(peerConnection.connectionState);
      }
    };

    const pendingCandidates: RTCIceCandidate[] = [];
    let connectionId: string | null = null;
    let offerResponse: OfferResponse | null = null;
    let endOfCandidates = false;
    const candidateQueue: Promise<unknown>[] = [];

    const flushCandidate = (
      candidates: RTCIceCandidate[],
      done: boolean,
    ): void => {
      if (!connectionId) return;
      candidateQueue.push(
        postICECandidates(
          options.ticket,
          options.sessionId,
          connectionId,
          candidates,
          done,
        ).catch(() => undefined),
      );
    };

    peerConnection.onicecandidate = (event) => {
      if (!event.candidate) {
        endOfCandidates = true;
        flushCandidate([], true);
        return;
      }

      if (!connectionId) {
        pendingCandidates.push(event.candidate);
        return;
      }

      flushCandidate([event.candidate], false);
    };

    const offer = await peerConnection.createOffer({
      offerToReceiveAudio: true,
    });
    await peerConnection.setLocalDescription(offer);

    offerResponse = await postWebRTCOffer(
      options.ticket,
      options.sessionId,
      offer,
    );
    connectionId = offerResponse.connection_id;

    await peerConnection.setRemoteDescription({
      type: (offerResponse.type as RTCSdpType) || "answer",
      sdp: offerResponse.sdp,
    });

    if (pendingCandidates.length > 0) {
      flushCandidate(pendingCandidates, false);
      pendingCandidates.length = 0;
    }
    if (endOfCandidates) {
      flushCandidate([], true);
    }
    await Promise.all(candidateQueue);

    // API /start requires realtime control-plane state === connected.
    await waitForPeerConnectionConnected(peerConnection, 20_000);
    await waitUntilRealtimeConnectionReady(
      options.ticket,
      options.sessionId,
      20_000,
    );
    await Promise.all([
      waitForDataChannelOpen(wakeWordChannel, 10_000),
      waitForDataChannelOpen(controlDataChannel, 10_000),
    ]);

    const pc = peerConnection;
    const stream = localStream;
    const audio = remoteAudio;
    const wakeChannel = wakeWordChannel;
    const controlChannel = controlDataChannel;
    return {
      connectionId,
      peerConnection: pc,
      localStream: stream,
      remoteAudio: audio,
      wakeWordChannel: wakeChannel,
      controlDataChannel: controlChannel,
      close: () => {
        closePartialSession({
          peerConnection: pc,
          localStream: stream,
          remoteAudio: audio,
          wakeWordChannel: wakeChannel,
          controlDataChannel: controlChannel,
        });
      },
    };
  } catch (error) {
    closePartialSession({
      peerConnection,
      localStream,
      remoteAudio,
      wakeWordChannel,
      controlDataChannel,
    });
    throw error;
  }
}
