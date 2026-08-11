import {
  DEFAULT_MODE,
  effectiveMode,
  isConnectionState,
  isMode,
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
}

export interface RuntimeClientOptions {
  reconnectPolicy?: ReconnectPolicy;
  sleep?: Sleep;
  createId?: () => string;
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
  private current: MobileState;
  private pendingOperations = new Set<string>();
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
    this.current = this.stateWith({
      sessionId,
      status: "idle",
      connection: null,
      runtime: null,
      mode: null,
      effectiveMode: DEFAULT_MODE,
      errorCode: null,
      staleOperationIds: [],
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
      this.transport.getMode(this.sessionId),
    ]);
    if (connection.status === "fulfilled") this.observeConnection(connection.value);
    if (runtime.status === "fulfilled") this.observeRuntime(runtime.value);
    if (mode.status === "fulfilled") this.observeMode(mode.value);

    const requiredFailure = [connection, runtime].find((result) => result.status === "rejected");
    if (requiredFailure?.status === "rejected") {
      this.update({ status: "error", errorCode: errorCode(requiredFailure.reason) });
    } else {
      const connectionState = this.current.connection?.state;
      this.update({
        status:
          connectionState === "disconnected" || connectionState === "failed"
            ? "reconnecting"
            : "ready",
      });
    }
    return this.current;
  }

  observeConnection(snapshot: ConnectionSnapshot): void {
    if (snapshot.session_id !== this.sessionId || !isConnectionState(snapshot.state)) {
      throw new Error("connection snapshot does not match session");
    }
    this.update({ connection: snapshot });
    if (snapshot.state === "disconnected" || snapshot.state === "failed") {
      this.update({ status: "reconnecting" });
    } else if (snapshot.state === "connected" && this.current.status === "reconnecting") {
      this.update({ status: "ready" });
    }
  }

  observeRuntime(snapshot: RuntimeSnapshot): void {
    if (snapshot.session_id !== this.sessionId || !isRuntimeState(snapshot.runtime_state)) {
      throw new Error("runtime snapshot does not match session");
    }
    this.update({ runtime: snapshot });
  }

  observeMode(snapshot: ModeStateSnapshot): void {
    if (
      snapshot.session_id !== this.sessionId ||
      !isMode(snapshot.active_mode) ||
      snapshot.generation < 1 ||
      !snapshot.runtime_instance_id
    ) {
      throw new Error("mode snapshot does not match the public contract");
    }
    if (
      this.current.mode &&
      this.current.mode.runtime_instance_id !== snapshot.runtime_instance_id
    ) {
      this.markPendingOperationsStale();
    }
    this.update({ mode: snapshot, effectiveMode: effectiveMode(snapshot) });
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
    this.update({ status: "syncing", errorCode: null });
    try {
      const result = await this.transport.switchMode(command);
      this.pendingOperations.delete(operationId);
      this.observeMode(result.state);
      this.update({ status: "ready" });
      return result;
    } catch (cause) {
      this.pendingOperations.delete(operationId);
      if (isModeConflict(cause)) {
        this.markOperationStale(operationId);
        const refreshedMode = await this.refreshMode();
        this.update({ status: "ready" });
        throw new ModeConflictError(cause.code, operationId, refreshedMode);
      }
      this.update({ status: "error", errorCode: errorCode(cause) });
      throw cause;
    }
  }

  async refreshMode(): Promise<ModeStateSnapshot | null> {
    try {
      const mode = await this.transport.getMode(this.sessionId);
      this.observeMode(mode);
      return mode;
    } catch (cause) {
      // Keep the last confirmed mode; with no snapshot this remains interpretation.
      this.update({ mode: this.current.mode, effectiveMode: effectiveMode(this.current.mode) });
      return null;
    }
  }

  isOperationStale(operationId: string): boolean {
    return this.current.staleOperationIds.includes(operationId);
  }

  async reconnect(signal?: AbortSignal): Promise<MobileState> {
    this.update({ status: "reconnecting", errorCode: null });
    for (let attempt = 1; ; attempt += 1) {
      if (signal?.aborted) throw new DOMException("reconnect aborted", "AbortError");
      const decision = this.policy.next(attempt, this.current);
      if (!decision.continue) {
        this.update({ status: "error", errorCode: "reconnect_exhausted" });
        return this.current;
      }
      await this.sleep(decision.waitMs);
      try {
      try {
        const connection = await this.transport.getConnection(this.sessionId);
        this.observeConnection(connection);
        if (connection.state === "connected") {
          const [runtime, mode] = await Promise.allSettled([
            this.transport.getRuntime(this.sessionId),
            this.transport.getMode(this.sessionId),
          ]);
          if (runtime.status === "fulfilled") this.observeRuntime(runtime.value);
          if (mode.status === "fulfilled") this.observeMode(mode.value);
          this.update({ status: "ready", errorCode: null });
          return this.current;
        }
      } catch (cause) {
        if (signal?.aborted) throw new DOMException("reconnect aborted", "AbortError");
        this.update({ errorCode: errorCode(cause) });
      }
      } catch (cause) {
        this.update({ status: "reconnecting", errorCode: errorCode(cause) });
      }
    }
  }

  private markPendingOperationsStale(): void {
    for (const operationId of this.pendingOperations) this.markOperationStale(operationId);
    this.pendingOperations.clear();
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

function isModeConflict(error: unknown): error is RealtimeApiError {
  return (
    error instanceof RealtimeApiError &&
    (error.code === "mode_generation_conflict" ||
      error.code === "mode_runtime_instance_mismatch" ||
      error.code === "mode_operation_conflict")
  );
}

function errorCode(error: unknown): string | null {
  return error instanceof RealtimeApiError ? error.code : error instanceof Error ? error.message : null;
}

function defaultId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `mobile_${Date.now()}_${Math.random().toString(36).slice(2)}`;
}
