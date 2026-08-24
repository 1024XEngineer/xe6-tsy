import type { NextConfig } from "next";

const apiBase =
  process.env.LINGOW_API_BASE_URL?.replace(/\/$/, "") ??
  "http://127.0.0.1:8080";

const realtimeBase =
  process.env.LINGOW_REALTIME_BASE_URL?.replace(/\/$/, "") ??
  "http://127.0.0.1:8090";

const staticExport = process.env.NEXT_STATIC_EXPORT === "1";
const basePath = process.env.NEXT_PUBLIC_BASE_PATH?.replace(/\/$/, "") ?? "";

const nextConfig: NextConfig = {
  devIndicators: false,
  ...(staticExport
    ? {
        output: "export" as const,
        trailingSlash: true,
        basePath,
        assetPrefix: basePath ? `${basePath}/` : undefined,
        images: { unoptimized: true },
      }
    : {
        async headers() {
          return [
            {
              // Unversioned filenames under /kws/wasm are overwritten by sync-kws-models;
              // avoid immutable/year-long cache so JS/WASM pairs stay coherent.
              source: "/kws/wasm/:path*",
              headers: [
                {
                  key: "Cache-Control",
                  value: "public, max-age=300, must-revalidate",
                },
              ],
            },
          ];
        },
        async rewrites() {
          return [
            {
              source: "/api/v1/:path*",
              destination: `${apiBase}/api/v1/:path*`,
            },
            {
              source: "/realtime/v1/:path*",
              destination: `${realtimeBase}/realtime/v1/:path*`,
            },
          ];
        },
      }),
};

export default nextConfig;
