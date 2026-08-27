import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

vi.mock("./site-shell", () => ({
  BackToTop: () => null,
  Reveal: ({ children }: { children: React.ReactNode }) => children,
  SiteFooter: () => null,
  SiteNav: () => null,
}));

import IntroPage from "./page";
import { AboutPage, DocumentationPage, ProductPage } from "./subpage";

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

  it("keeps product and about sections focused on useful labels", () => {
    const { unmount } = render(<ProductPage />);

    expect(screen.getByRole("heading", { name: /一条实时会话/ })).toBeInTheDocument();
    expect(screen.getByText(/为面对面交流提供一条临时的实时会话/)).toBeInTheDocument();
    expect(screen.getByText("工作方式")).toBeInTheDocument();
    expect(screen.getByText("实时链路")).toBeInTheDocument();
    expect(screen.queryByText("PRODUCT / 01")).not.toBeInTheDocument();
    expect(screen.queryByText("01 / MODES")).not.toBeInTheDocument();
    expect(screen.queryByText("SESSION / P0")).not.toBeInTheDocument();

    unmount();
    render(<AboutPage />);

    expect(screen.getByText(/把复杂度留在实时链路和协议边界里/)).toBeInTheDocument();
    expect(screen.getByText("产品原则")).toBeInTheDocument();
    expect(screen.getByText("当前阶段")).toBeInTheDocument();
    expect(screen.queryByText("ABOUT LINGOW / 01")).not.toBeInTheDocument();
    expect(screen.queryByText("02 / ARCHITECTURE")).not.toBeInTheDocument();
  });
});
