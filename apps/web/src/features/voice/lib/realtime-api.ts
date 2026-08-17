import { newIdempotencyKey, parseJson } from "./http";

export type WebRTCConfig = {
  session_id: string;
  expires_at: string;
  ice_servers: Array<{
    urls: string[];
    username?: string;
    credential?: string;
  }>;
  ice_transport_policy: string;
  data_channel: { label: string; ordered: boolean };
  control_data_channel?: {
    label: string;
    ordered: boolean;
    protocol_version: number;
  };
  audio: {
    uplink_codec: string;
    downlink_codec: string;
    sample_rate_hz: number;
    channels: number;
  };
};

export type OfferResponse = {
  sdp: string;
  type: string;
  session_id: string;
  connection_id: string;
  data_channel_label: string;
  tts_track_id: string;
  connection_state: string;
};

export type CandidateResponse = {
  connection_id: string;
  accepted_candidate_ids: string[];
  deduplicated_candidate_ids: string[];
  end_of_candidates: boolean;
};

export type ConnectionSnapshot = {
  session_id: string;
  connection_id: string;
  state: RealtimeConnectionState;
  version: number;
  updated_at: string;
};

export type RealtimeConnectionState =
  | "new"
  | "connecting"
  | "connected"
  | "disconnected"
  | "failed"
  | "closed";

export type RealtimeMode = "assistant" | "interpretation";

export type ModePhase = "active" | "switching";

export type ModeStateSnapshot = {
  session_id: string;
  runtime_instance_id: string;
  active_mode: RealtimeMode;
  generation: number;
  phase: ModePhase;
  last_operation_id: string | null;
  updated_at: string;
};

export type SwitchModeCommand = {
  session_id: string;
  runtime_instance_id: string;
  operation_id: string;
  trace_id: string;
  expected_generation: number;
  target_mode: RealtimeMode;
};

export type ModeSwitchStatus = "applied" | "unchanged";

export type SwitchModeResult = {
  operation_id: string;
  status: ModeSwitchStatus;
  state: ModeStateSnapshot;
};

function ticketHeaders(
  ticket: string,
  idempotencyKey?: string,
): HeadersInit {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${ticket}`,
  };
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return headers;
}

/** Local-only helper. Requires ENABLE_DEV_REALTIME_TICKET=true in next dev. */
export async function mintDevRealtimeTicket(
  sessionId: string,
  accountId: string,
): Promise<string> {
  const response = await fetch("/api/dev/realtime-ticket", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      session_id: sessionId,
      account_id: accountId,
    }),
  });
  const data = await parseJson<{ ticket: string }>(response);
  return data.ticket;
}

export async function getWebRTCConfig(
  ticket: string,
  sessionId: string,
): Promise<WebRTCConfig> {
  const response = await fetch(
    `/realtime/v1/sessions/${encodeURIComponent(sessionId)}/webrtc/config`,
    {
      headers: ticketHeaders(ticket),
      cache: "no-store",
    },
  );
  return parseJson<WebRTCConfig>(response);
}

export async function postWebRTCOffer(
  ticket: string,
  sessionId: string,
  offer: RTCSessionDescriptionInit,
): Promise<OfferResponse> {
  const response = await fetch(
    `/realtime/v1/sessions/${encodeURIComponent(sessionId)}/webrtc/offer`,
    {
      method: "POST",
      headers: {
        ...ticketHeaders(ticket, newIdempotencyKey("offer")),
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        sdp: offer.sdp ?? "",
        type: offer.type ?? "offer",
      }),
    },
  );
  return parseJson<OfferResponse>(response);
}

export async function postICECandidates(
  ticket: string,
  sessionId: string,
  connectionId: string,
  candidates: RTCIceCandidate[],
  endOfCandidates: boolean,
): Promise<CandidateResponse> {
  const response = await fetch(
    `/realtime/v1/sessions/${encodeURIComponent(sessionId)}/ice-candidates`,
    {
      method: "POST",
      headers: {
        ...ticketHeaders(ticket),
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        connection_id: connectionId,
        candidates: candidates.map((candidate) => ({
          candidate_id: newIdempotencyKey("ice"),
          candidate: candidate.candidate,
          sdp_mid: candidate.sdpMid,
          sdp_mline_index: candidate.sdpMLineIndex,
          username_fragment: candidate.usernameFragment,
        })),
        end_of_candidates: endOfCandidates,
      }),
    },
  );
  return parseJson<CandidateResponse>(response);
}

export async function getRealtimeConnection(
  ticket: string,
  sessionId: string,
): Promise<ConnectionSnapshot> {
  const response = await fetch(
    `/realtime/v1/sessions/${encodeURIComponent(sessionId)}/connection`,
    {
      headers: ticketHeaders(ticket),
      cache: "no-store",
    },
  );
  return parseJson<ConnectionSnapshot>(response);
}

/**
 * Reads the authoritative mode snapshot owned by realtime-audio.
 * The browser keeps this as an observation only; it never locally increments
 * generation or treats an API projection as the source of truth.
 */
export async function getRealtimeModeState(
  ticket: string,
  sessionId: string,
): Promise<ModeStateSnapshot> {
  const response = await fetch(
    `/realtime/v1/sessions/${encodeURIComponent(sessionId)}/mode`,
    {
      headers: ticketHeaders(ticket),
      cache: "no-store",
    },
  );
  return parseJson<ModeStateSnapshot>(response);
}

/**
 * Sends one typed compare-and-switch command. A caller must use the snapshot's
 * runtime instance and generation; conflict responses are intentionally left
 * to the caller so it can refresh without replaying a stale operation.
 */
export async function switchRealtimeMode(
  ticket: string,
  sessionId: string,
  command: SwitchModeCommand,
  idempotencyKey = newIdempotencyKey("mode"),
): Promise<SwitchModeResult> {
  const response = await fetch(
    `/realtime/v1/sessions/${encodeURIComponent(sessionId)}/mode`,
    {
      method: "POST",
      headers: {
        ...ticketHeaders(ticket, idempotencyKey),
        "Content-Type": "application/json",
      },
      body: JSON.stringify(command),
    },
  );
  return parseJson<SwitchModeResult>(response);
}

export async function waitUntilRealtimeConnectionReady(
  ticket: string,
  sessionId: string,
  timeoutMs = 20_000,
): Promise<ConnectionSnapshot> {
  const deadline = Date.now() + timeoutMs;
  let lastState = "unknown";
  while (Date.now() < deadline) {
    const snapshot = await getRealtimeConnection(ticket, sessionId);
    lastState = snapshot.state;
    if (snapshot.state === "connected") {
      return snapshot;
    }
    if (
      snapshot.state === "failed" ||
      snapshot.state === "closed" ||
      snapshot.state === "disconnected"
    ) {
      throw new Error(
        `WebRTC 连接失败（realtime state=${snapshot.state}）`,
      );
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(
    `等待 WebRTC connected 超时（最后状态=${lastState}）。API /start 要求 realtime 侧为 connected。`,
  );
}
