import { expect, test, type APIResponse } from "@playwright/test";

type AuthResult = {
  account: { id: string; kind: string };
  tokens: { access_token: string; refresh_token: string; expires_at: string };
};

type Session = { id: string; status: string; account_id: string };

async function expectJSON<T>(
  response: APIResponse,
  status: number,
): Promise<T> {
  expect(response.status(), await response.text()).toBe(status);
  return (await response.json()) as T;
}

test("runs the real API, realtime and WebRTC session path", async ({
  page,
  request,
}) => {
  test.setTimeout(90_000);
  const phone = `+861380${Date.now().toString().slice(-7)}`;
  const challengeResponse = await request.post(
    "/api/v1/auth/verification-codes",
    {
      data: { phone },
    },
  );
  const challenge = await expectJSON<{ challenge_id: string }>(
    challengeResponse,
    202,
  );

  const loginResponse = await request.post("/api/v1/auth/phone/login", {
    data: { challenge_id: challenge.challenge_id, code: "888888" },
  });
  const auth = await expectJSON<AuthResult>(loginResponse, 200);
  expect(auth.account.kind).toBe("registered");

  const authHeaders = { Authorization: `Bearer ${auth.tokens.access_token}` };
  const sessionResponse = await request.post("/api/v1/voice-sessions", {
    headers: {
      ...authHeaders,
      "Idempotency-Key": `system-create-${Date.now()}`,
    },
    data: {
      audio_config: {
        codec: "opus",
        sample_rate_hz: 48000,
        channels: 1,
        echo_cancellation: true,
        noise_suppression: true,
        auto_gain_control: true,
      },
      capabilities: {
        webrtc: true,
        data_channel: true,
        microphone: true,
        speaker: true,
        speaker_diarization: true,
      },
    },
  });
  const session = await expectJSON<Session>(sessionResponse, 201);
  expect(session.account_id).toBe(auth.account.id);
  expect(session.status).toBe("created");

  const languageResponse = await request.post(
    `/api/v1/voice-sessions/${session.id}/language-configs`,
    {
      headers: {
        ...authHeaders,
        "Idempotency-Key": `system-language-${Date.now()}`,
      },
      data: {
        languages: [
          { source: "zh-CN", target: "en-US" },
          { source: "en-US", target: "zh-CN" },
        ],
      },
    },
  );
  await expectJSON(languageResponse, 201);

  const ticketResponse = await request.post(
    `/api/v1/voice-sessions/${session.id}/realtime-ticket`,
    { headers: authHeaders },
  );
  const ticket = await expectJSON<{ ticket: string; session_id: string }>(
    ticketResponse,
    200,
  );
  expect(ticket.session_id).toBe(session.id);

  await page.goto("/");
  const connection = await page.evaluate(
    async ({ sessionId, accessTicket }) => {
      const authHeader = { Authorization: `Bearer ${accessTicket}` };
      const configResponse = await fetch(
        `/realtime/v1/sessions/${sessionId}/webrtc/config`,
        {
          headers: authHeader,
        },
      );
      if (!configResponse.ok)
        throw new Error(`config failed: ${configResponse.status}`);
      const config = (await configResponse.json()) as {
        ice_servers: RTCIceServer[];
        data_channel: { label: string; ordered: boolean };
        control_data_channel?: { label: string; ordered: boolean };
      };

      const peer = new RTCPeerConnection({ iceServers: config.ice_servers });
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: true,
        video: false,
      });
      for (const track of stream.getTracks()) peer.addTrack(track, stream);
      peer.createDataChannel(config.data_channel.label, {
        ordered: config.data_channel.ordered,
      });
      peer.createDataChannel(
        config.control_data_channel?.label ?? "lingow-control-v1",
        {
          ordered: config.control_data_channel?.ordered ?? true,
        },
      );

      const pendingCandidates: RTCIceCandidateInit[] = [];
      let connectionId = "";
      let candidatesDone: Promise<unknown> = Promise.resolve();
      peer.onicecandidate = (event) => {
        if (!event.candidate) return;
        const candidate = event.candidate.toJSON();
        if (!connectionId) {
          pendingCandidates.push(candidate);
          return;
        }
        candidatesDone = candidatesDone.then(() =>
          fetch(`/realtime/v1/sessions/${sessionId}/ice-candidates`, {
            method: "POST",
            headers: { ...authHeader, "Content-Type": "application/json" },
            body: JSON.stringify({
              connection_id: connectionId,
              candidates: [
                {
                  candidate_id: crypto.randomUUID(),
                  candidate: candidate.candidate,
                  sdp_mid: candidate.sdpMid,
                  sdp_mline_index: candidate.sdpMLineIndex,
                  username_fragment: candidate.usernameFragment,
                },
              ],
              end_of_candidates: false,
            }),
          }),
        );
      };

      await peer.setLocalDescription(
        await peer.createOffer({ offerToReceiveAudio: true }),
      );
      await new Promise<void>((resolve) => {
        if (peer.iceGatheringState === "complete") return resolve();
        peer.addEventListener("icegatheringstatechange", () => {
          if (peer.iceGatheringState === "complete") resolve();
        });
      });

      const offerResponse = await fetch(
        `/realtime/v1/sessions/${sessionId}/webrtc/offer`,
        {
          method: "POST",
          headers: {
            ...authHeader,
            "Content-Type": "application/json",
            "Idempotency-Key": `system-offer-${crypto.randomUUID()}`,
          },
          body: JSON.stringify({
            sdp: peer.localDescription?.sdp ?? "",
            type: "offer",
          }),
        },
      );
      if (!offerResponse.ok)
        throw new Error(`offer failed: ${offerResponse.status}`);
      const answer = (await offerResponse.json()) as {
        connection_id: string;
        sdp: string;
        type: string;
      };
      connectionId = answer.connection_id;
      await peer.setRemoteDescription({
        type: answer.type as RTCSdpType,
        sdp: answer.sdp,
      });

      if (pendingCandidates.length > 0) {
        await fetch(`/realtime/v1/sessions/${sessionId}/ice-candidates`, {
          method: "POST",
          headers: { ...authHeader, "Content-Type": "application/json" },
          body: JSON.stringify({
            connection_id: connectionId,
            candidates: pendingCandidates.map((candidate) => ({
              candidate_id: crypto.randomUUID(),
              candidate: candidate.candidate,
              sdp_mid: candidate.sdpMid,
              sdp_mline_index: candidate.sdpMLineIndex,
              username_fragment: candidate.usernameFragment,
            })),
            end_of_candidates: false,
          }),
        });
      }
      await candidatesDone;

      await new Promise<void>((resolve, reject) => {
        const deadline = window.setTimeout(
          () => reject(new Error(`peer state: ${peer.connectionState}`)),
          20_000,
        );
        const check = () => {
          if (peer.connectionState === "connected") {
            window.clearTimeout(deadline);
            resolve();
          } else if (["failed", "closed"].includes(peer.connectionState)) {
            window.clearTimeout(deadline);
            reject(new Error(`peer state: ${peer.connectionState}`));
          }
        };
        peer.addEventListener("connectionstatechange", check);
        check();
      });

      const deadline = Date.now() + 20_000;
      let realtimeState = "";
      while (Date.now() < deadline) {
        const response = await fetch(
          `/realtime/v1/sessions/${sessionId}/connection`,
          {
            headers: authHeader,
          },
        );
        const snapshot = (await response.json()) as { state: string };
        realtimeState = snapshot.state;
        if (snapshot.state === "connected") break;
        await new Promise((resolve) => window.setTimeout(resolve, 250));
      }
      if (realtimeState !== "connected")
        throw new Error(`realtime state: ${realtimeState}`);
      return { connectionId, peerState: peer.connectionState, realtimeState };
    },
    { sessionId: session.id, accessTicket: ticket.ticket },
  );

  expect(connection.peerState).toBe("connected");
  expect(connection.realtimeState).toBe("connected");

  const startResponse = await request.post(
    `/api/v1/voice-sessions/${session.id}/start`,
    {
      headers: {
        ...authHeaders,
        "Idempotency-Key": `system-start-${Date.now()}`,
      },
      data: { initial_mode: "assistant" },
    },
  );
  const started = await expectJSON<Session>(startResponse, 200);
  expect(started.status).toBe("active");

  const endResponse = await request.post(
    `/api/v1/voice-sessions/${session.id}/end`,
    {
      headers: {
        ...authHeaders,
        "Idempotency-Key": `system-end-${Date.now()}`,
      },
    },
  );
  const ended = await expectJSON<Session>(endResponse, 200);
  expect(ended.status).toBe("ended");
});
