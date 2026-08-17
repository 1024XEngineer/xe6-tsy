import { describe, expect, it } from "vitest";

import { parseCommandResult } from "./command-results";

function appliedResult(): Record<string, unknown> {
  return {
    type: "command.result",
    event_version: 1,
    command_id: "wake-1",
    session_id: "session-1",
    runtime_instance_id: "runtime-1",
    generation: 2,
    status: "applied",
    action: "activate_mode",
    target_mode: "interpretation",
    message: "已进入同声传译模式",
    occurred_at: "2026-08-12T10:00:00Z",
  };
}

describe("parseCommandResult", () => {
  it.each(["applied", "unchanged"] as const)(
    "parses a complete %s execution result",
    (status) => {
      const payload = { ...appliedResult(), status };
      expect(parseCommandResult(payload)).toEqual(payload);
    },
  );

  it.each(["clarification_required", "unsupported", "failed"] as const)(
    "parses a %s result without execution fields",
    (status) => {
      const payload = {
        type: "command.result",
        event_version: 1,
        command_id: "wake-2",
        session_id: "session-1",
        status,
        message: "本次指令未执行",
        occurred_at: "2026-08-12T10:00:00.123+08:00",
      };
      expect(parseCommandResult(payload)).toEqual(payload);
    },
  );

  it("accepts a failure with a complete runtime snapshot", () => {
    const payload = {
      ...appliedResult(),
      status: "failed",
      message: "依赖调用失败",
    };
    expect(parseCommandResult(payload)).toEqual(payload);
  });

  it.each([
    ["missing runtime", { runtime_instance_id: undefined }],
    ["missing generation", { generation: undefined }],
    ["missing action", { action: undefined }],
    ["missing target mode", { target_mode: undefined }],
    ["zero generation", { generation: 0 }],
    ["fractional generation", { generation: 1.5 }],
    ["unknown target mode", { target_mode: "english_practice" }],
    ["empty action", { action: "" }],
  ])("rejects a successful result with %s", (_, replacement) => {
    const payload = appliedResult();
    const [field, fieldValue] = Object.entries(replacement)[0]!;
    if (fieldValue === undefined) delete payload[field];
    else payload[field] = fieldValue;
    expect(parseCommandResult(payload)).toBeNull();
  });

  it.each([
    [{ status: "failed", runtime_instance_id: "runtime-1" }],
    [{ status: "failed", generation: 2 }],
    [{ status: "failed", runtime_instance_id: "", generation: 2 }],
    [{ status: "failed", runtime_instance_id: "runtime-1", generation: -1 }],
  ])(
    "rejects an invalid non-success runtime field combination",
    (execution) => {
      const payload = {
        type: "command.result",
        event_version: 1,
        command_id: "wake-2",
        session_id: "session-1",
        message: "执行失败",
        occurred_at: "2026-08-12T10:00:00Z",
        ...execution,
      };
      expect(parseCommandResult(payload)).toBeNull();
    },
  );

  it.each([
    ["unrelated event", { type: "translation.final" }],
    ["array payload", []],
    ["unknown field", { ...appliedResult(), trace_id: "trace-1" }],
    ["leading ID whitespace", { ...appliedResult(), command_id: " wake-1" }],
    ["overlong ID", { ...appliedResult(), command_id: "界".repeat(129) }],
    ["message line break", { ...appliedResult(), message: "执行\n成功" }],
    ["overlong message", { ...appliedResult(), message: "答".repeat(513) }],
    [
      "invalid calendar date",
      { ...appliedResult(), occurred_at: "2026-02-30T10:00:00Z" },
    ],
    ["non-RFC3339 date", { ...appliedResult(), occurred_at: "2026-08-12" }],
    ["unknown status", { ...appliedResult(), status: "pending" }],
  ])("rejects %s", (_, payload) => {
    expect(parseCommandResult(payload)).toBeNull();
  });
});
