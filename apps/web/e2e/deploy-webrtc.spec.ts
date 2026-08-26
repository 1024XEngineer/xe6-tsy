import { expect, test, type APIResponse } from "@playwright/test";

type Session = { id: string; status: string; account_id: string };

async function expectJSON<T>(response: APIResponse, status: number): Promise<T> {
  expect(response.status(), await response.text()).toBe(status);
  return (await response.json()) as T;
}

test("connects an external browser through TURN and realtime", async ({ page, request }) => {
  test.setTimeout(90_000);
  const token = process.env.DEPLOY_SMOKE_ACCESS_TOKEN;
  if (!token) throw new Error("DEPLOY_SMOKE_ACCESS_TOKEN is required");
  const authHeaders = { Authorization: `Bearer ${token}` };
  const idempotency = () => `deploy-smoke-${crypto.randomUUID()}`;

  const sessionResponse = await request.post("/api/v1/voice-sessions", {
    headers: { ...authHeaders, "Idempotency-Key": idempotency() },
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

  try {
    const languageResponse = await request.post(
      `/api/v1/voice-sessions/${session.id}/language-configs`,
      {
        headers: { ...authHeaders, "Idempotency-Key": idempotency() },
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
        const auth = { Authorization: `Bearer ${accessTicket}` };
        const configResponse = await fetch(
          `/realtime/v1/sessions/${sessionId}/webrtc/config`,
          { headers: auth },
        );
        if (!configResponse.ok) throw new Error(`config failed: ${configResponse.status}`);
        const config = (await configResponse.json()) as {
          ice_servers: RTCIceServer[];
          data_channel: { label: string; ordered: boolean };
          control_data_channel?: { label: string; ordered: boolean };
        };
        if (!config.ice_servers.some((server) =>
          (Array.isArray(server.urls) ? server.urls : [server.urls]).some((url) =>
            url.startsWith("turn:") || url.startsWith("turns:"),
          ),
        )) throw new Error("TURN server missing from WebRTC config");

        const peer = new RTCPeerConnection({ iceServers: config.ice_servers });
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        for (const track of stream.getTracks()) peer.addTrack(track, stream);
        const events = peer.createDataChannel(config.data_channel.label, {
          ordered: config.data_channel.ordered,
        });
        const control = peer.createDataChannel(config.control_data_channel?.label ?? "lingow-control-v1", {
          ordered: config.control_data_channel?.ordered ?? true,
        });
        const eventOpen = new Promise<void>((resolve, reject) => {
          const deadline = window.setTimeout(() => reject(new Error("event DataChannel did not open")), 20_000);
          events.onopen = () => {
            window.clearTimeout(deadline);
            resolve();
          };
          events.onerror = () => {
            window.clearTimeout(deadline);
            reject(new Error("event DataChannel failed"));
          };
        });
        const controlOpen = new Promise<void>((resolve, reject) => {
          const deadline = window.setTimeout(() => reject(new Error("control DataChannel did not open")), 20_000);
          control.onopen = () => {
            window.clearTimeout(deadline);
            resolve();
          };
          control.onerror = () => {
            window.clearTimeout(deadline);
            reject(new Error("control DataChannel failed"));
          };
        });
        const controlResponse = new Promise<{ error?: { code?: string } }>((resolve, reject) => {
          const deadline = window.setTimeout(() => reject(new Error("control DataChannel response timed out")), 20_000);
          control.onmessage = (message) => {
            window.clearTimeout(deadline);
            try {
              resolve(JSON.parse(String(message.data)) as { error?: { code?: string } });
            } catch {
              reject(new Error("control DataChannel returned invalid JSON"));
            }
          };
        });
        const pending: RTCIceCandidateInit[] = [];
        let connectionId = "";
        let candidatePosts: Promise<Response> = Promise.resolve(new Response());
        peer.onicecandidate = (event) => {
          if (!event.candidate) return;
          const candidate = event.candidate.toJSON();
          if (!connectionId) {
            pending.push(candidate);
            return;
          }
          candidatePosts = candidatePosts.then(async () => {
            const response = await fetch(`/realtime/v1/sessions/${sessionId}/ice-candidates`, {
              method: "POST",
              headers: { ...auth, "Content-Type": "application/json" },
              body: JSON.stringify({
                connection_id: connectionId,
                candidates: [{
                  candidate_id: crypto.randomUUID(),
                  candidate: candidate.candidate,
                  sdp_mid: candidate.sdpMid,
                  sdp_mline_index: candidate.sdpMLineIndex,
                  username_fragment: candidate.usernameFragment,
                }],
                end_of_candidates: false,
              }),
            });
            if (!response.ok) throw new Error(`candidate post failed: ${response.status}`);
            return response;
          });
        };

        await peer.setLocalDescription(await peer.createOffer({ offerToReceiveAudio: true }));
        await new Promise<void>((resolve) => {
          if (peer.iceGatheringState === "complete") return resolve();
          peer.addEventListener("icegatheringstatechange", () => {
            if (peer.iceGatheringState === "complete") resolve();
          });
        });
        const offerResponse = await fetch(`/realtime/v1/sessions/${sessionId}/webrtc/offer`, {
          method: "POST",
          headers: { ...auth, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
          body: JSON.stringify({ sdp: peer.localDescription?.sdp ?? "", type: "offer" }),
        });
        if (!offerResponse.ok) throw new Error(`offer failed: ${offerResponse.status}`);
        const answer = (await offerResponse.json()) as { connection_id: string; sdp: string; type: RTCSdpType };
        connectionId = answer.connection_id;
        await peer.setRemoteDescription({ type: answer.type, sdp: answer.sdp });
        if (pending.length) {
          const response = await fetch(`/realtime/v1/sessions/${sessionId}/ice-candidates`, {
            method: "POST",
            headers: { ...auth, "Content-Type": "application/json" },
            body: JSON.stringify({
              connection_id: connectionId,
              candidates: pending.map((candidate) => ({
                candidate_id: crypto.randomUUID(),
                candidate: candidate.candidate,
                sdp_mid: candidate.sdpMid,
                sdp_mline_index: candidate.sdpMLineIndex,
                username_fragment: candidate.usernameFragment,
              })),
              end_of_candidates: false,
            }),
          });
          if (!response.ok) throw new Error(`candidate post failed: ${response.status}`);
        }
        await candidatePosts;
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
        await Promise.all([eventOpen, controlOpen]);
        control.send("{}");
        const response = await controlResponse;
        if (!response.error?.code) throw new Error("control DataChannel round-trip returned no protocol error");
        const stateDeadline = Date.now() + 20_000;
        let realtimeState = "";
        while (Date.now() < stateDeadline) {
          const response = await fetch(`/realtime/v1/sessions/${sessionId}/connection`, { headers: auth });
          if (!response.ok) throw new Error(`connection state failed: ${response.status}`);
          realtimeState = ((await response.json()) as { state: string }).state;
          if (realtimeState === "connected") break;
          await new Promise((resolve) => window.setTimeout(resolve, 250));
        }
        if (realtimeState !== "connected") throw new Error(`realtime state: ${realtimeState}`);
        const candidateStats = await peer.getStats();
        const candidates = new Map<string, { candidateType?: string }>();
        candidateStats.forEach((report) => {
          if (report.type === "local-candidate" || report.type === "remote-candidate") {
            candidates.set(report.id, report as { candidateType?: string });
          }
        });
        let selectedRelay = false;
        candidateStats.forEach((report) => {
          if (report.type === "candidate-pair" && report.state === "succeeded") {
            const pair = report as RTCIceCandidatePairStats;
            selectedRelay ||= candidates.get(pair.localCandidateId)?.candidateType === "relay";
            selectedRelay ||= candidates.get(pair.remoteCandidateId)?.candidateType === "relay";
          }
        });
        peer.close();
        for (const track of stream.getTracks()) track.stop();
        return { connectionId, peerState: peer.connectionState, realtimeState, selectedRelay, controlRoundTrip: true };
      },
      { sessionId: session.id, accessTicket: ticket.ticket },
    );
    expect(connection.peerState).toBe("closed");
    expect(connection.realtimeState).toBe("connected");
    expect(connection.selectedRelay).toBe(true);
    expect(connection.controlRoundTrip).toBe(true);
  } finally {
    const endResponse = await request.post(`/api/v1/voice-sessions/${session.id}/end`, {
      headers: { ...authHeaders, "Idempotency-Key": idempotency() },
    });
    expect([200, 409]).toContain(endResponse.status());
  }
});
