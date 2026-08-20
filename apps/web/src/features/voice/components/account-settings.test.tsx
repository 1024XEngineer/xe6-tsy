import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { saveAuthSession } from "../lib/auth-session";
import { AccountSettings } from "./account-settings";

describe("AccountSettings", () => {
  beforeEach(() => {
    localStorage.clear();
    saveAuthSession({
      account: {
        id: "acc-account",
        kind: "registered",
        created_at: "2026-08-01T00:00:00Z",
      },
      tokens: {
        access_token: "access-account",
        refresh_token: "refresh-account",
        expires_at: "2099-08-01T00:00:00Z",
      },
    });
  });

  it("shows the authenticated account", async () => {
    render(<AccountSettings />);

    expect(screen.getByText("已登录")).toBeInTheDocument();
    expect(screen.getByText("acc-account")).toBeInTheDocument();
    expect(screen.getByText("2026/8/1")).toBeInTheDocument();
  });

  it("calls the logout action", async () => {
    const onLogout = vi.fn();
    render(<AccountSettings onLogout={onLogout} />);

    fireEvent.click(screen.getByRole("button", { name: "退出登录" }));
    await waitFor(() => expect(onLogout).toHaveBeenCalledTimes(1));
  });

  it("disables logout during an active conversation", () => {
    render(<AccountSettings logoutDisabled onLogout={vi.fn()} />);

    expect(screen.getByRole("button", { name: "退出登录" })).toBeDisabled();
    expect(
      screen.getByText("请先结束当前对话，再退出登录。"),
    ).toBeInTheDocument();
  });
});
