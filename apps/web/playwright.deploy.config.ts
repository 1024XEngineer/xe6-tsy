import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.DEPLOY_PUBLIC_BASE_URL;
if (!baseURL) {
  throw new Error("DEPLOY_PUBLIC_BASE_URL is required for deployment WebRTC smoke");
}

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/deploy-webrtc.spec.ts",
  outputDir: "./test-results/deploy",
  reporter: "line",
  workers: 1,
  use: {
    ...devices["Desktop Chrome"],
    baseURL,
    ignoreHTTPSErrors: true,
    permissions: ["microphone"],
    launchOptions: {
      args: ["--use-fake-device-for-media-stream", "--use-fake-ui-for-media-stream"],
    },
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
});
