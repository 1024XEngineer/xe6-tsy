import { expect, test } from "@playwright/test";

const voiceButton = (page: import("@playwright/test").Page) =>
  page.getByRole("button", { name: /语音会话/ });

async function mockLingowApis(page: import("@playwright/test").Page) {
  await page.route("**/api/v1/auth/anonymous", async (route) => {
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        account: {
          id: "acc-e2e",
          kind: "anonymous",
          created_at: "2026-07-31T00:00:00Z",
        },
        tokens: {
          access_token: "access-e2e",
          refresh_token: "refresh-e2e",
          expires_at: "2026-07-31T01:00:00Z",
        },
      }),
    });
  });

  await page.route("**/api/v1/voice-sessions", async (route) => {
    if (route.request().method() !== "POST") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        id: "vs-e2e",
        account_id: "acc-e2e",
        status: "created",
        created_at: "2026-07-31T00:00:00Z",
      }),
    });
  });

  await page.route("**/language-configs", async (route) => {
    await route.fulfill({
      status: 201,
      contentType: "application/json",
      body: JSON.stringify({
        id: "lc-e2e",
        session_id: "vs-e2e",
        version: 1,
        language_pairs: [
          { source: "zh-CN", target: "en-US" },
          { source: "en-US", target: "zh-CN" },
        ],
        status: "active",
        effective_from: "2026-07-31T00:00:00Z",
        created_by: "acc-e2e",
        created_at: "2026-07-31T00:00:00Z",
      }),
    });
  });

  await page.route("**/realtime-ticket", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        ticket: "v1.e2e.ticket",
        session_id: "vs-e2e",
        expires_at: "2026-07-31T00:01:00Z",
      }),
    });
  });

  await page.route("**/realtime/v1/**", async (route) => {
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "realtime_unavailable",
          message: "realtime control-plane is not running in e2e mock",
        },
      }),
    });
  });

  await page.addInitScript(() => {
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: {
        getUserMedia: async () =>
          ({
            getTracks: () => [{ stop: () => undefined }],
          }) as MediaStream,
      },
    });
  });
}

test("keeps the Lingow experience inside one viewport", async ({ page }) => {
  await page.goto("/");

  await expect(page.getByText("lingow", { exact: true })).toBeVisible();
  await expect(voiceButton(page)).toBeVisible();
  await expect(page.getByRole("button", { name: "设置" })).toBeVisible();
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollHeight <= window.innerHeight,
      ),
    )
    .toBe(true);
});

test("shows API errors clearly when realtime is unavailable", async ({
  page,
}) => {
  await mockLingowApis(page);
  await page.goto("/");
  await voiceButton(page).click();

  await expect(page.getByText("联调失败")).toBeVisible({ timeout: 8_000 });
  await expect(page.getByText(/realtime/i)).toBeVisible();
});

test("keeps voice states legible with reduced motion", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");

  await expect(page.locator("main")).toHaveCSS(
    "background-color",
    "rgba(0, 0, 0, 0)",
  );
  await expect(page.getByTestId("idle-voice-ring")).toBeVisible();
  await expect(page.getByText("轻触开始")).toBeVisible();
});

test("renders painted pixels for the idle voice canvas", async ({ page }) => {
  const hasPaintedPixels = async (selector: string) =>
    page.locator(selector).evaluate((canvas: HTMLCanvasElement) => {
      const context = canvas.getContext("2d");
      if (!context || canvas.width === 0 || canvas.height === 0) return false;
      const pixels = context.getImageData(0, 0, canvas.width, canvas.height).data;
      return pixels.some((channel, index) => index % 4 === 3 && channel > 0);
    });

  await page.goto("/");
  await expect
    .poll(() => hasPaintedPixels('[data-testid="idle-voice-ring"] canvas'))
    .toBe(true);
});
