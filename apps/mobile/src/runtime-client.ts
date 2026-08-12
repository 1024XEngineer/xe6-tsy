import {
  LEGACY_MODE_FALLBACK,
  effectiveMode,
  isConnectionState,
  isMode,
  isModePhase,
  isModeSwitchStatus,
  isRuntimeState,
  type ConnectionSnapshot,
  type ConnectionState,
  type Mode,
  type ModeStateSnapshot,
  type RuntimeSnapshot,
  type RuntimeState,
  type SwitchModeCommand,
  type SwitchModeResult,
} from "./contracts.ts";
import {
  ExponentialReconnectPolicy,
  realSleep,
  type ReconnectPolicy,
  type Sleep,
} from "./reconnect.ts";
import { RealtimeApiError, type RealtimeTransport } from "./transport.ts";

export type ClientStatus = "idle" | "syncing" | "ready" | "reconnecting" | "error";
export type ModeCommandStatus = "pending" | "applied" | "unchanged" | "conflict" | "failed";

export interface ModeCommandState {
  operationId: string;
  targetMode: Mode;
  status: ModeCommandStatus;
  errorCode: string | null;
}

export interface MobileState {
  sessionId: string;
  status: ClientStatus;
  connection: ConnectionSnapshot | null;
  runtime: RuntimeSnapshot | null;
  mode: ModeStateSnapshot | null;
  /** Compatibility projection; never implies that a mode snapshot was received. */
  effectiveMode: Mode;
  errorCode: string | null;
  staleOperationIds: readonly string[];
  lastModeCommand: ModeCommandState | null;
}

export interface RuntimeClientOptions {
  reconnectPolicy?: ReconnectPolicy;
  sleep?: Sleep;
  createId?: () => string;
  /** Performs one real platform WebRTC reconnect or ICE restart attempt. */
  reconnectMedia?: (signal?: AbortSignal) => Promise<void>;
}

export class ModeConflictError extends Error {
  readonly code: string | null;
  readonly refreshedMode: ModeStateSnapshot | null;
  readonly staleOperationId: string;

  constructor(code: string | null, operationId: string, refreshedMode: ModeStateSnapshot | null) {
    super("mode command used a stale runtime snapshot");
    this.name = "ModeConflictError";
    this.code = code;
    this.staleOperationId = operationId;
    this.refreshedMode = refreshedMode;
  }
}

type Listener = (state: MobileState) => void;

export class RuntimeClient {
  private readonly listeners = new Set<Listener>();
  private readonly policy: ReconnectPolicy;
  private readonly sleep: Sleep;
  private readonly createId: () => string;
  private readonly reconnectMedia: ((signal?: AbortSignal) => Promise<void>) | null;
  private current: MobileState;
  private pendingOperations = new Set<string>();
  private readonly retiredConnectionIds = new Set<string>();
  private readonly retiredStartOperationIds = new Set<string>();
  private readonly retiredRuntimeIds = new Set<string>();
  private modeReadSequence = 0;
  readonly sessionId: string;
  private readonly transport: RealtimeTransport;

  constructor(
    sessionId: string,
    transport: RealtimeTransport,
    options: RuntimeClientOptions = {},
  ) {
    if (!sessionId.trim()) throw new Error("sessionId is required");
    this.sessionId = sessionId;
    this.transport = transport;
    this.policy = options.reconnectPolicy ?? new ExponentialReconnectPolicy();
    this.sleep = options.sleep ?? realSleep;
    this.createId = options.createId ?? defaultId;
    this.reconnectMedia = options.reconnectMedia ?? null;
    this.current = this.stateWith({
      sessionId,
      status: "idle",
      connection: null,
      runtime: null,
      mode: null,
      effectiveMode: LEGACY_MODE_FALLBACK,
      errorCode: null,
      staleOperationIds: [],
      lastModeCommand: null,
    });
  }

  get state(): MobileState {
    return this.current;
  }

  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    listener(this.current);
    return () => this.listeners.delete(listener);
  }

  async sync(): Promise<MobileState> {
    this.update({ status: "syncing", errorCode: null });
    const [connection, runtime, mode] = await Promise.allSettled([
      this.transport.getConnection(this.sessionId),
      this.transport.getRuntime(this.sessionId),
      this.readMode(),
    ]);
    const observationFailures: unknown[] = [];
    if (connection.status === "fulfilled") {
      try { this.observeConnection(connection.value); } catch (cause) { observationFailures.push(cause); }
    }
    if (runtime.status === "fulfilled") {
      try { this.observeRuntime(runtime.value); } catch (cause) { observationFailures.push(cause); }
    }

    const requestFailure = [connection, runtime, mode].find((result) => result.status === "rejected");
    const failure = observationFailures[0] ??
      (requestFailure?.status === "rejected" ? requestFailure.reason : null);
    if (failure) {
      this.update({ status: "error", errorCode: errorCode(failure) });
    } else {
      const connectionState = this.current.connection?.state;
      this.update({
        status: clientStatusForConnection(connectionState),
        errorCode:
          connectionState === "failed" || connectionState === "closed"
            ? `connection_${connectionState}`
            : null,
      });
    }
    return this.current;
  }

  observeConnection(snapshot: ConnectionSnapshot): boolean {
    if (
      snapshot.session_id !== this.sessionId ||
      !snapshot.connection_id ||
      !isConnectionState(snapshot.state) ||
      !Number.isInteger(snapshot.version) ||
      snapshot.version < 1 ||
      !isValidTimestamp(snapshot.updated_at)
    ) {
      throw new Error("connection snapshot does not match session");
    }
    const previous = this.current.connection;
    if (previous) {
      if (previous.connection_id === snapshot.connection_id) {
        if (snapshot.version <= previous.version) return false;
      } else {
        if (
          this.retiredConnectionIds.has(snapshot.connection_id) ||
          !isNewerTimestamp(previous.updated_at, snapshot.updated_at)
        ) {
          return false;
        }
        this.retiredConnectionIds.add(previous.connection_id);
      }
    }
    this.update({ connection: snapshot });
    if (snapshot.state === "closed") {
      this.update({ status: "error", errorCode: "connection_closed" });
    } else if (snapshot.state === "disconnected" || snapshot.state === "failed") {
      this.update({ status: "reconnecting" });
    } else if (snapshot.state === "connected" && this.current.status === "reconnecting") {
      this.update({ status: "ready" });
    }
    return true;
  }

  observeRuntime(snapshot: RuntimeSnapshot): boolean {
    if (
      snapshot.session_id !== this.sessionId ||
      !snapshot.start_operation_id ||
      !isRuntimeState(snapshot.runtime_state) ||
      !isValidTimestamp(snapshot.updated_at)
    ) {
      throw new Error("runtime snapshot does not match session");
    }
    const previous = this.current.runtime;
    if (previous) {
      if (previous.start_operation_id === snapshot.start_operation_id) {
        if (!isNewerTimestamp(previous.updated_at, snapshot.updated_at)) return false;
      } else {
        if (
          this.retiredStartOperationIds.has(snapshot.start_operation_id) ||
          !isNewerTimestamp(previous.updated_at, snapshot.updated_at)
        ) {
          return false;
        }
        this.retiredStartOperationIds.add(previous.start_operation_id);
      }
    }
    this.update({ runtime: snapshot });
    return true;
  }

  observeMode(snapshot: ModeStateSnapshot): boolean {
    this.modeReadSequence += 1;
    return this.observeModeSnapshot(snapshot);
  }

  private observeModeSnapshot(snapshot: ModeStateSnapshot): boolean {
    if (
      snapshot.session_id !== this.sessionId ||
      !isMode(snapshot.active_mode) ||
      !isModePhase(snapshot.phase) ||
      !Number.isInteger(snapshot.generation) ||
      snapshot.generation < 1 ||
      !snapshot.runtime_instance_id ||
      !isValidTimestamp(snapshot.updated_at)
    ) {
      throw new Error("mode snapshot does not match the public contract");
    }
    const previous = this.current.mode;
    if (previous) {
      if (previous.runtime_instance_id === snapshot.runtime_instance_id) {
        if (
          snapshot.generation < previous.generation ||
          (snapshot.generation === previous.generation &&
            !isNewerTimestamp(previous.updated_at, snapshot.updated_at))
        ) {
          return false;
        }
      } else {
        if (this.retiredRuntimeIds.has(snapshot.runtime_instance_id)) {
          return false;
        }
        this.retiredRuntimeIds.add(previous.runtime_instance_id);
        this.markPendingOperationsStale();
      }
    }
    this.update({ mode: snapshot, effectiveMode: effectiveMode(snapshot) });
    return true;
  }

  async switchMode(targetMode: Mode, traceId = this.createId()): Promise<SwitchModeResult> {
    if (!isMode(targetMode)) throw new Error("unsupported mode");
    const mode = this.current.mode;
    if (!mode) {
      await this.refreshMode();
    }
    const currentMode = this.current.mode;
    if (!currentMode) throw new Error("mode snapshot is unavailable");
    const operationId = this.createId();
    const command: SwitchModeCommand = {
      session_id: this.sessionId,
      runtime_instance_id: currentMode.runtime_instance_id,
      operation_id: operationId,
      trace_id: traceId,
      expected_generation: currentMode.generation,
      target_mode: targetMode,
    };
    this.pendingOperations.add(operationId);
    this.update({
      status: "syncing",
      errorCode: null,
      lastModeCommand: { operationId, targetMode, status: "pending", errorCode: null },
    });
    try {
      const result = await this.transport.switchMode(command);
      if (
        result.operation_id !== operationId ||
        !isModeSwitchStatus(result.status) ||
        result.state.session_id !== this.sessionId ||
        result.state.runtime_instance_id !== command.runtime_instance_id ||
        result.state.active_mode !== targetMode ||
        result.state.phase !== "active" ||
        result.state.last_operation_id !== operationId ||
        (result.status === "applied" &&
          result.state.generation !== command.expected_generation + 1) ||
        (result.status === "unchanged" &&
          result.state.generation !== command.expected_generation)
      ) {
        throw new Error("mode response does not match operation");
      }
      this.pendingOperations.delete(operationId);
      const accepted = this.observeMode(result.state);
      const observed = this.current.mode;
      const responseIsCurrent = Boolean(
        observed &&
          observed.runtime_instance_id === result.state.runtime_instance_id &&
          observed.generation === result.state.generation &&
          observed.active_mode === result.state.active_mode,
      );
      if (this.isOperationStale(operationId) || (!accepted && !responseIsCurrent)) {
        this.markOperationStale(operationId);
        this.update({
          status: "ready",
          lastModeCommand: { operationId, targetMode, status: "conflict", errorCode: "stale_mode_response" },
        });
        throw new ModeConflictError("stale_mode_response", operationId, observed);
      }
      this.update({
        status: "ready",
        lastModeCommand: { operationId, targetMode, status: result.status, errorCode: null },
      });
      return result;
    } catch (cause) {
      this.pendingOperations.delete(operationId);
      if (cause instanceof ModeConflictError) {
        // A late success was already compared with the newer authoritative
        // snapshot above. Preserve that snapshot and its ready projection.
        throw cause;
      }
      if (isModeConflict(cause)) {
        this.markOperationStale(operationId);
        this.update({
          lastModeCommand: { operationId, targetMode, status: "conflict", errorCode: cause.code },
        });
        const refreshedMode = await this.refreshMode();
        this.update({ status: "ready" });
        throw new ModeConflictError(cause.code, operationId, refreshedMode);
      }
      const code = errorCode(cause);
      this.update({
        status: "error",
        errorCode: code,
        lastModeCommand: { operationId, targetMode, status: "failed", errorCode: code },
      });
      throw cause;
    }
  }

  async refreshMode(): Promise<ModeStateSnapshot | null> {
    try {
      return await this.readMode();
    } catch (cause) {
      this.update({ status: "error", errorCode: errorCode(cause) });
      throw cause;
    }
  }

  isOperationStale(operationId: string): boolean {
    return this.current.staleOperationIds.includes(operationId);
  }

  async reconnect(signal?: AbortSignal): Promise<MobileState> {
    if (this.connectionIsClosed()) {
      this.update({ status: "error", errorCode: "connection_closed" });
      return this.current;
    }
    this.update({ status: "reconnecting", errorCode: null });
    if (!this.reconnectMedia) {
      this.update({ status: "error", errorCode: "media_reconnect_unavailable" });
      return this.current;
    }
    for (let attempt = 1; ; attempt += 1) {
      if (signal?.aborted) throw new DOMException("reconnect aborted", "AbortError");
      const decision = this.policy.next(attempt, this.current);
      if (!decision.continue) {
        this.update({ status: "error", errorCode: "reconnect_exhausted" });
        return this.current;
      }
      await this.sleep(decision.waitMs);
      try {
        await this.reconnectMedia(signal);
        const connection = await this.transport.getConnection(this.sessionId);
        this.observeConnection(connection);
        if (this.connectionIsClosed()) return this.current;
        if (this.current.connection?.state === "connected") {
          const runtime = await this.transport.getRuntime(this.sessionId);
          this.observeRuntime(runtime);
          await this.refreshMode();
          this.update({ status: "ready", errorCode: null });
          return this.current;
        }
      } catch (cause) {
        if (signal?.aborted) throw new DOMException("reconnect aborted", "AbortError");
        if (this.connectionIsClosed()) return this.current;
        if (this.current.connection?.state === "connected") {
          this.update({ status: "error", errorCode: errorCode(cause) });
          return this.current;
        }
        this.update({ status: "reconnecting", errorCode: errorCode(cause) });
      }
    }
  }

  private markPendingOperationsStale(): void {
    for (const operationId of this.pendingOperations) this.markOperationStale(operationId);
    this.pendingOperations.clear();
  }

  private connectionIsClosed(): boolean {
    return this.current.connection?.state === "closed";
  }

  private async readMode(): Promise<ModeStateSnapshot | null> {
    const sequence = ++this.modeReadSequence;
    try {
      const snapshot = await this.transport.getMode(this.sessionId);
      if (sequence !== this.modeReadSequence) return this.current.mode;
      this.observeModeSnapshot(snapshot);
      return this.current.mode;
    } catch (cause) {
      if (isLegacyModeUnavailable(cause)) return this.current.mode;
      throw cause;
    }
  }

  private markOperationStale(operationId: string): void {
    if (this.current.staleOperationIds.includes(operationId)) return;
    this.update({ staleOperationIds: [...this.current.staleOperationIds, operationId] });
  }

  private update(patch: Partial<MobileState>): void {
    this.current = this.stateWith({ ...this.current, ...patch });
    for (const listener of this.listeners) listener(this.current);
  }

  private stateWith(state: MobileState): MobileState {
    return { ...state, staleOperationIds: [...state.staleOperationIds] };
  }
}

function clientStatusForConnection(
  state: ConnectionState | undefined,
): ClientStatus {
  switch (state) {
    case "connected":
      return "ready";
    case "disconnected":
      return "reconnecting";
    case "failed":
    case "closed":
      return "error";
    case "new":
    case "connecting":
    default:
      return "syncing";
  }
}

function isNewerTimestamp(previous: string, next: string): boolean {
  return Date.parse(next) > Date.parse(previous);
}

function isValidTimestamp(value: string): boolean {
  return typeof value === "string" && Number.isFinite(Date.parse(value));
}

function isModeConflict(error: unknown): error is RealtimeApiError {
  return (
    error instanceof RealtimeApiError &&
    (error.code === "mode_generation_conflict" ||
      error.code === "mode_runtime_instance_mismatch" ||
      error.code === "mode_operation_conflict")
  );
}

function isLegacyModeUnavailable(error: unknown): boolean {
  return error instanceof RealtimeApiError && error.status === 501 && error.code === "not_implemented";
}

function errorCode(error: unknown): string | null {
  return error instanceof RealtimeApiError ? error.code : error instanceof Error ? error.message : null;
}

function defaultId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `mobile_${Date.now()}_${Math.random().toString(36).slice(2)}`;
}
