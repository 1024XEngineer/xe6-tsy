import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { LocaleProvider, Localized, useLocale } from "./locale";

function LocaleFixture() {
  const { locale, setLocale } = useLocale();
  return (
    <>
      <button type="button" onClick={() => setLocale(locale === "zh" ? "en" : "zh")}>toggle</button>
      <Localized>
        <section>
          <h1>让两种语言，</h1>
          <p>Web 当前可运行</p>
        </section>
      </Localized>
    </>
  );
}

describe("intro locale", () => {
  it("switches localized content between Chinese and English", () => {
    render(<LocaleProvider><LocaleFixture /></LocaleProvider>);

    expect(screen.getByRole("heading", { name: "让两种语言，" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "toggle" }));
    expect(screen.getByRole("heading", { name: "Let two languages" })).toBeInTheDocument();
    expect(screen.getByText("Web is currently runnable")).toBeInTheDocument();
  });
});
