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
  dataChannel: RTCDataChannel | null;
  close: () => void;
};

export type WebRTCSessionOptions = {
  ticket: string;
  sessionId: string;
  onDataMessage?: (payload: unknown) => void;
  onConnectionStateChange?: (state: RTCPeerConnectionState) => void;
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

export async function openWebRTCSession(
  options: WebRTCSessionOptions,
): Promise<WebRTCSessionHandles> {
  const config = await getWebRTCConfig(options.ticket, options.sessionId);
  const peerConnection = new RTCPeerConnection({
    iceServers: toIceServers(config.ice_servers),
    iceTransportPolicy:
      config.ice_transport_policy === "relay" ? "relay" : "all",
  });

  const localStream = await navigator.mediaDevices.getUserMedia({
    audio: {
      echoCancellation: true,
      noiseSuppression: true,
      autoGainControl: true,
    },
    video: false,
  });

  for (const track of localStream.getTracks()) {
    peerConnection.addTrack(track, localStream);
  }

  const remoteAudio = document.createElement("audio");
  remoteAudio.autoplay = true;
  remoteAudio.setAttribute("playsinline", "true");

  peerConnection.ontrack = (event) => {
    const [stream] = event.streams;
    if (stream) {
      remoteAudio.srcObject = stream;
      void remoteAudio.play().catch(() => {
        // Autoplay may be blocked until a user gesture; the click that started
        // the session usually counts, but ignore failures here.
      });
    }
  };

  const label = config.data_channel.label || "translation-events";
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
  // Local client channel (control). Server also opens translation-events;
  // subscribe via ondatachannel so mock FinalTurn events are received.
  const dataChannel = peerConnection.createDataChannel(label, {
    ordered: config.data_channel.ordered,
  });
  bindDataChannel(dataChannel);
  peerConnection.ondatachannel = (event) => {
    if (event.channel.label === label || event.channel.label === "translation-events") {
      bindDataChannel(event.channel);
    }
  };

  peerConnection.onconnectionstatechange = () => {
    options.onConnectionStateChange?.(peerConnection.connectionState);
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

  return {
    connectionId,
    peerConnection,
    localStream,
    remoteAudio,
    dataChannel,
    close: () => {
      try {
        dataChannel.close();
      } catch {
        // ignore
      }
      for (const track of localStream.getTracks()) {
        track.stop();
      }
      peerConnection.close();
      remoteAudio.srcObject = null;
      remoteAudio.remove();
    },
  };
}
