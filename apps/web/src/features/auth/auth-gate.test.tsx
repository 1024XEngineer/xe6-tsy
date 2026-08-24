import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { saveAuthSession } from "@/features/voice/lib/auth-session";
import {
  loginWithPhone,
  logoutAccount,
  requestPhoneVerificationCode,
} from "@/features/voice/lib/lingow-api";

import { AuthGate } from "./auth-gate";

vi.mock("@/features/voice/components/voice-experience", () => ({
  VoiceExperience: ({ onLogout }: { onLogout: () => void }) => (
    <button onClick={onLogout} type="button">
      已登录，退出
    </button>
  ),
}));

vi.mock("@/features/voice/lib/lingow-api", async (importOriginal) => {
  const original = await importOriginal<
    typeof import("@/features/voice/lib/lingow-api")
  >();
  return {
    ...original,
    loginWithPhone: vi.fn(),
    logoutAccount: vi.fn(),
    requestPhoneVerificationCode: vi.fn(),
  };
});

const registeredAuth = {
  account: {
    id: "acc-login",
    kind: "registered" as const,
    created_at: "2026-08-20T00:00:00Z",
  },
  tokens: {
    access_token: "access-login",
    refresh_token: "refresh-login",
    expires_at: "2099-08-20T01:00:00Z",
  },
};

describe("AuthGate", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.mocked(requestPhoneVerificationCode).mockReset();
    vi.mocked(loginWithPhone).mockReset();
    vi.mocked(logoutAccount).mockReset();
  });

  it("requests a code and logs in with a canonical mainland phone number", async () => {
    vi.mocked(requestPhoneVerificationCode).mockResolvedValue({
      challenge_id: "challenge-1",
    });
    vi.mocked(loginWithPhone).mockResolvedValue(registeredAuth);

    render(<AuthGate />);
    const phoneInput = await screen.findByLabelText("手机号码");
    fireEvent.change(phoneInput, { target: { value: "13800000000" } });
    fireEvent.click(screen.getByRole("button", { name: "获取验证码" }));

    expect(await screen.findByLabelText("验证码")).toBeVisible();
    expect(requestPhoneVerificationCode).toHaveBeenCalledWith("+8613800000000");

    fireEvent.change(screen.getByLabelText("验证码"), {
      target: { value: "8888" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));

    expect(await screen.findByRole("button", { name: "已登录，退出" })).toBeVisible();
    expect(loginWithPhone).toHaveBeenCalledWith("challenge-1", "8888");
    expect(localStorage.getItem("lingow-auth-session-v1")).toContain(
      '\"kind\":\"registered\"',
    );
  });

  it("rejects an invalid phone before making a request", async () => {
    render(<AuthGate />);
    const phoneInput = await screen.findByLabelText("手机号码");
    fireEvent.change(phoneInput, { target: { value: "123" } });
    fireEvent.click(screen.getByRole("button", { name: "获取验证码" }));

    expect(screen.getByText("请输入正确的 11 位手机号码")).toBeVisible();
    expect(requestPhoneVerificationCode).not.toHaveBeenCalled();
  });

  it("clears local credentials even when server logout fails", async () => {
    saveAuthSession(registeredAuth);
    vi.mocked(logoutAccount).mockRejectedValue(new Error("offline"));

    render(<AuthGate />);
    fireEvent.click(await screen.findByRole("button", { name: "已登录，退出" }));

    await waitFor(() => expect(screen.getByLabelText("手机号码")).toBeVisible());
    expect(localStorage.getItem("lingow-auth-session-v1")).toBeNull();
    expect(logoutAccount).toHaveBeenCalledWith("refresh-login");
  });
});
