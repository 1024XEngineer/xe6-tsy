import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

vi.mock("./site-shell", () => ({
  BackToTop: () => null,
  Reveal: ({ children }: { children: React.ReactNode }) => children,
  SiteFooter: () => null,
  SiteNav: () => null,
}));

import IntroPage from "./page";
import { DocumentationPage } from "./subpage";

describe("IntroPage", () => {
  it("presents the current homepage story and switches between work modes", () => {
    render(<IntroPage />);

    expect(screen.getByRole("heading", { name: /让两种语言/ })).toBeInTheDocument();
    expect(screen.getByText("Web 当前可运行")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /面对面同传/ })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "AI 语音助手" }));

    expect(screen.getByRole("heading", { name: /AI 语音助手/ })).toBeInTheDocument();
    expect(screen.getByText("本地唤醒词“小灵小灵”")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "AI 语音助手" })).toHaveAttribute("aria-selected", "true");
  });

  it("offers copy feedback and a collapsible documentation directory", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });

    render(<DocumentationPage />);

    const copyButton = screen.getAllByRole("button", { name: /复制/ })[0];
    fireEvent.click(copyButton);
    expect(await screen.findByRole("button", { name: /已复制/ })).toBeInTheDocument();
    expect(writeText).toHaveBeenCalled();

    const menuButton = screen.getByRole("button", { name: "展开目录" });
    fireEvent.click(menuButton);
    expect(screen.getByRole("button", { name: "收起目录" })).toHaveAttribute("aria-expanded", "true");
  });
});
