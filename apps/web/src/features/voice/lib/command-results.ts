import type {
  CommandResultEvent as ContractCommandResultEvent,
  CommandResultStatus,
  RealtimeMode,
} from "../../../../../../packages/contracts/typescript/realtime";

export type CommandResultEvent = ContractCommandResultEvent;

const allowedFields = new Set([
  "type",
  "event_version",
  "command_id",
  "session_id",
  "runtime_instance_id",
  "generation",
  "status",
  "action",
  "target_mode",
  "message",
  "occurred_at",
]);

const statuses = new Set<CommandResultStatus>([
  "applied",
  "unchanged",
  "clarification_required",
  "unsupported",
  "failed",
]);

const modes = new Set<RealtimeMode>(["assistant", "interpretation"]);

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  return value as Record<string, unknown>;
}

function hasOwn(value: Record<string, unknown>, field: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, field);
}

function hasValidUnicode(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const unit = value.charCodeAt(index);
    if (unit >= 0xd800 && unit <= 0xdbff) {
      const next = value.charCodeAt(index + 1);
      if (next < 0xdc00 || next > 0xdfff) return false;
      index += 1;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) {
      return false;
    }
  }
  return true;
}

function validText(value: unknown, maxCodePoints?: number): value is string {
  return (
    typeof value === "string" &&
    value.length > 0 &&
    value.trim() === value &&
    !/[\r\n\t]/u.test(value) &&
    hasValidUnicode(value) &&
    (maxCodePoints === undefined || [...value].length <= maxCodePoints)
  );
}

function validRFC3339(value: unknown): value is string {
  if (typeof value !== "string") return false;
  const match =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|([+-])(\d{2}):(\d{2}))$/u.exec(
      value,
    );
  if (!match) return false;

  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const hour = Number(match[4]);
  const minute = Number(match[5]);
  const second = Number(match[6]);
  const offsetHour = match[8] === undefined ? 0 : Number(match[8]);
  const offsetMinute = match[9] === undefined ? 0 : Number(match[9]);
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const daysInMonth = [
    31,
    leapYear ? 29 : 28,
    31,
    30,
    31,
    30,
    31,
    31,
    30,
    31,
    30,
    31,
  ];

  return (
    month >= 1 &&
    month <= 12 &&
    day >= 1 &&
    day <= daysInMonth[month - 1]! &&
    hour <= 23 &&
    minute <= 59 &&
    second <= 59 &&
    offsetHour <= 23 &&
    offsetMinute <= 59 &&
    Number.isFinite(Date.parse(value))
  );
}

/** Parse only the closed, strict v1 command acknowledgement contract. */
export function parseCommandResult(
  payload: unknown,
): CommandResultEvent | null {
  const value = asRecord(payload);
  if (!value || Object.keys(value).some((field) => !allowedFields.has(field))) {
    return null;
  }
  if (value.type !== "command.result" || value.event_version !== 1) return null;
  if (!validText(value.command_id, 128) || !validText(value.session_id, 128)) {
    return null;
  }
  if (!validText(value.message, 512) || !validRFC3339(value.occurred_at)) {
    return null;
  }
  if (
    typeof value.status !== "string" ||
    !statuses.has(value.status as CommandResultStatus)
  ) {
    return null;
  }

  const hasRuntime = hasOwn(value, "runtime_instance_id");
  const hasGeneration = hasOwn(value, "generation");
  const hasAction = hasOwn(value, "action");
  const hasTargetMode = hasOwn(value, "target_mode");
  if (hasRuntime && !validText(value.runtime_instance_id)) return null;
  if (
    hasGeneration &&
    (typeof value.generation !== "number" ||
      !Number.isSafeInteger(value.generation) ||
      value.generation < 1)
  ) {
    return null;
  }
  if (hasAction && !validText(value.action, 64)) return null;
  if (
    hasTargetMode &&
    (typeof value.target_mode !== "string" ||
      !modes.has(value.target_mode as RealtimeMode))
  ) {
    return null;
  }

  const status = value.status as CommandResultStatus;
  if (status === "applied" || status === "unchanged") {
    if (!hasRuntime || !hasGeneration || !hasAction || !hasTargetMode)
      return null;
  } else if (hasRuntime !== hasGeneration) {
    return null;
  }

  return value as unknown as CommandResultEvent;
}
