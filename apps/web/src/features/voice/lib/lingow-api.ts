import {
  bilingualPairs,
  outputRoutes,
  type InterpretationOutputMode,
  type VoiceSessionConfig,
} from "./languages";
import { ApiError, newIdempotencyKey, parseJson } from "./http";

export type AuthTokens = {
  access_token: string;
  refresh_token: string;
  expires_at: string;
};

export type AuthResult = {
  account: { id: string; kind: string; created_at: string };
  tokens: AuthTokens;
};

export type VoiceSession = {
  id: string;
  account_id: string;
  status: "created" | "active" | "ended" | "failed";
  created_at: string;
  started_at?: string | null;
  ended_at?: string | null;
};

export type VoiceSessionListResponse = {
  sessions: VoiceSession[];
  next_cursor: string | null;
};

export type RuntimeState =
  | "stopped"
  | "starting"
  | "listening"
  | "asr_processing"
  | "translating"
  | "tts_processing"
  | "playing"
  | "stopping"
  | "failed";

export type VoiceSessionDetail = VoiceSession & {
  runtime_state: RuntimeState;
  current_turn_id: string | null;
  current_playback_id: string | null;
  last_error_code: string | null;
  retryable: boolean;
  runtime_updated_at: string;
};

export type VoiceSessionStateSnapshot = {
  session_id: string;
  status: VoiceSession["status"];
  runtime_state: RuntimeState;
  current_turn_id: string | null;
  current_playback_id: string | null;
  last_error_code: string | null;
  retryable: boolean;
  runtime_updated_at: string;
};

export type VoiceTurn = {
  id: string;
  session_id: string;
  source_language: string;
  target_language: string;
  source_text: string;
  translated_text: string;
  sequence_no: number;
  created_at: string;
};

export type VoiceTurnListResponse = {
  items: VoiceTurn[];
  next_cursor: string | null;
};

export type LanguageConfigResponse = {
  id: string;
  session_id: string;
  version: number;
  language_pairs: Array<{ source: string; target: string }>;
  output_routes: Array<{
    target_language: string;
    tts_enabled: boolean;
    delivery_enabled: boolean;
  }>;
  output_mode: InterpretationOutputMode;
  status: "active" | "superseded" | "expired";
  effective_from: string;
  effective_until: string | null;
  created_by: string;
  created_at: string;
};

export type SupportedLanguage = {
  language_code: string;
  display_name: string;
  display_name_en: string;
  supports_as_source: boolean;
  supports_as_target: boolean;
};

export type SupportedLanguageListResponse = {
  languages: SupportedLanguage[];
};

export type UsageStageTotal = {
  service_type: "asr" | "translation" | "tts" | "diarization" | string;
  input_tokens: number;
  output_tokens: number;
  audio_duration_ms: number;
  cost_amount: string;
  currency: string;
};

export type UsageSummary = {
  account_id: string;
  period_start: string;
  period_end: string;
  totals: UsageStageTotal[];
};

function authHeaders(accessToken: string, idempotencyKey?: string): HeadersInit {
  const headers: Record<string, string> = {
    Authorization: `Bearer ${accessToken}`,
  };
  if (idempotencyKey) {
    headers["Idempotency-Key"] = idempotencyKey;
  }
  return headers;
}

export async function createAnonymousAccount(): Promise<AuthResult> {
  const response = await fetch("/api/v1/auth/anonymous", {
    method: "POST",
  });
  return parseJson<AuthResult>(response);
}

export async function createVoiceSession(
  accessToken: string,
): Promise<VoiceSession> {
  const response = await fetch("/api/v1/voice-sessions", {
    method: "POST",
    headers: {
      ...authHeaders(accessToken, newIdempotencyKey("create")),
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
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
    }),
  });
  return parseJson<VoiceSession>(response);
}

export async function createLanguageConfig(
  accessToken: string,
  sessionId: string,
  config: VoiceSessionConfig,
  expectedVersion?: number,
): Promise<LanguageConfigResponse> {
  const response = await fetch(
    `/api/v1/voice-sessions/${encodeURIComponent(sessionId)}/language-configs`,
    {
      method: "POST",
      headers: {
        ...authHeaders(accessToken, newIdempotencyKey("lang")),
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        languages: bilingualPairs(config),
        output_routes: outputRoutes(config),
        ...(expectedVersion === undefined
          ? {}
          : { expected_version: expectedVersion }),
      }),
    },
  );
  return parseJson<LanguageConfigResponse>(response);
}

export async function getCurrentLanguageConfig(
  accessToken: string,
  sessionId: string,
): Promise<LanguageConfigResponse> {
  const response = await fetch(
    `/api/v1/voice-sessions/${encodeURIComponent(sessionId)}/language-config`,
    {
      headers: authHeaders(accessToken),
      cache: "no-store",
    },
  );
  return parseJson<LanguageConfigResponse>(response);
}

export type RealtimeTicket = {
  ticket: string;
  session_id: string;
  expires_at: string;
};

/** Mint a short-lived realtime HMAC ticket from the product API (session owner). */
export async function mintRealtimeTicket(
  accessToken: string,
  sessionId: string,
): Promise<RealtimeTicket> {
  const response = await fetch(
    `/api/v1/voice-sessions/${encodeURIComponent(sessionId)}/realtime-ticket`,
    {
      method: "POST",
      headers: authHeaders(accessToken),
    },
  );
  return parseJson<RealtimeTicket>(response);
}

export async function startVoiceSession(
  accessToken: string,
  sessionId: string,
  idempotencyKey = newIdempotencyKey("start"),
  signal?: AbortSignal,
): Promise<VoiceSession> {
  for (let attempt = 0; attempt < 2; attempt += 1) {
    signal?.throwIfAborted();
    try {
      const response = await fetch(
        `/api/v1/voice-sessions/${encodeURIComponent(sessionId)}/start`,
        {
          method: "POST",
          headers: authHeaders(accessToken, idempotencyKey),
          signal,
        },
      );
      return await parseJson<VoiceSession>(response);
    } catch (error) {
      if (!isRetryableStartError(error) || attempt === 1) {
        throw error;
      }
      await waitForRetry(signal, 250);
    }
  }
  throw new Error("unreachable");
}

export async function listVoiceSessions(
  accessToken: string,
  query: {
    limit?: number;
    cursor?: string;
    status?: VoiceSession["status"];
  } = {},
): Promise<VoiceSessionListResponse> {
  const params = new URLSearchParams({ limit: String(query.limit ?? 20) });
  if (query.cursor) params.set("cursor", query.cursor);
  if (query.status) params.set("status", query.status);
  const response = await fetch(`/api/v1/voice-sessions?${params}`, {
    headers: authHeaders(accessToken),
    cache: "no-store",
  });
  return parseJson<VoiceSessionListResponse>(response);
}

export async function listSupportedLanguages(
  accessToken: string,
): Promise<SupportedLanguageListResponse> {
  const response = await fetch("/api/v1/languages?active=true", {
    headers: authHeaders(accessToken),
    cache: "no-store",
  });
  return parseJson<SupportedLanguageListResponse>(response);
}

export async function hasReadyAutomaticTarget(
  accessToken: string,
): Promise<boolean> {
  const response = await fetch("/api/v1/account/message-preferences", {
    headers: authHeaders(accessToken),
    cache: "no-store",
  });
  const result = await parseJson<{
    items: Array<{
      channel: string;
      destination_ref: string;
      enabled: boolean;
      verified: boolean;
    }>;
  }>(response);
  return result.items.some(
    (preference) =>
      (preference.channel === "email" || preference.channel === "wechat") &&
      preference.enabled &&
      preference.verified &&
      preference.destination_ref.trim() !== "",
  );
}

export async function getAccountUsageSummary(
  accessToken: string,
  periodStart: string,
  periodEnd: string,
): Promise<UsageSummary> {
  const params = new URLSearchParams({
    period_start: periodStart,
    period_end: periodEnd,
  });
  const response = await fetch(`/api/v1/usage/summary?${params}`, {
    headers: authHeaders(accessToken),
    cache: "no-store",
  });
  return parseJson<UsageSummary>(response);
}

export async function refreshAccountTokens(
  refreshToken: string,
): Promise<AuthTokens> {
  const response = await fetch("/api/v1/auth/token/refresh", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ refresh_token: refreshToken }),
  });
  return parseJson<AuthTokens>(response);
}

function waitForRetry(signal: AbortSignal | undefined, delayMs: number): Promise<void> {
  if (!signal) return new Promise((resolve) => setTimeout(resolve, delayMs));
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(signal.reason ?? new DOMException("The operation was aborted", "AbortError"));
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", onAbort);
      reject(signal.reason ?? new DOMException("The operation was aborted", "AbortError"));
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, delayMs);
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

function isRetryableStartError(error: unknown): boolean {
  if (!(error instanceof ApiError)) return false;
  if (error.status === 503) return true;
  return (
    error.status === 409 &&
    (error.code === "webrtc_not_ready" ||
      error.code === "realtime_start_failed" ||
      error.code === "session_start_in_progress")
  );
}

export async function endVoiceSession(
  accessToken: string,
  sessionId: string,
  reason: "user_requested" | "operator_cancelled" | "client_disconnected" = "user_requested",
): Promise<VoiceSession> {
  const response = await fetch(
    `/api/v1/voice-sessions/${encodeURIComponent(sessionId)}/end`,
    {
      method: "POST",
      headers: {
        ...authHeaders(accessToken, newIdempotencyKey("end")),
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ reason }),
    },
  );
  return parseJson<VoiceSession>(response);
}

export async function getVoiceSessionState(
  accessToken: string,
  sessionId: string,
): Promise<VoiceSessionStateSnapshot> {
  const response = await fetch(
    `/api/v1/voice-sessions/${encodeURIComponent(sessionId)}/state`,
    {
      headers: authHeaders(accessToken),
      cache: "no-store",
    },
  );
  return parseJson<VoiceSessionStateSnapshot>(response);
}

export async function listSessionTurns(
  accessToken: string,
  sessionId: string,
  limit = 50,
  cursor?: string,
): Promise<VoiceTurnListResponse> {
  const params = new URLSearchParams({ limit: String(limit) });
  if (cursor) params.set("cursor", cursor);
  const response = await fetch(
    `/api/v1/voice-sessions/${encodeURIComponent(sessionId)}/turns?${params}`,
    {
      headers: authHeaders(accessToken),
      cache: "no-store",
    },
  );
  return parseJson<VoiceTurnListResponse>(response);
}
