import { expect, test } from "@playwright/test";

const voiceButton = (page: import("@playwright/test").Page) =>
  page.getByRole("button", { name: /翻译/ });

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
  await expect(page.getByText(/轻触/)).toBeVisible();
});

test("renders the idle voice video", async ({ page }) => {
  await page.goto("/");
  const video = page.getByTestId("idle-voice-video");
  await expect(video).toHaveAttribute("src", "/media/loop.mp4");
  await expect
    .poll(() =>
      video.evaluate((element: HTMLVideoElement) => {
        if (
          element.readyState < HTMLMediaElement.HAVE_CURRENT_DATA ||
          element.videoWidth === 0 ||
          element.currentTime <= 0
        ) {
          return false;
        }
        const canvas = document.createElement("canvas");
        canvas.width = 32;
        canvas.height = 32;
        const context = canvas.getContext("2d", { willReadFrequently: true });
        if (!context) return false;
        context.drawImage(element, 0, 0, canvas.width, canvas.height);
        const pixels = context.getImageData(0, 0, canvas.width, canvas.height).data;
        for (let index = 0; index < pixels.length; index += 4) {
          if (
            pixels[index + 3] > 0 &&
            pixels[index] + pixels[index + 1] + pixels[index + 2] > 8
          ) {
            return true;
          }
        }
        return false;
      }),
    )
    .toBe(true);
});
