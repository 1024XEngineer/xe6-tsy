import type { Metadata, Viewport } from "next";
import { Geist, Noto_Sans_SC } from "next/font/google";

import "./globals.css";

const geist = Geist({
  display: "swap",
  subsets: ["latin"],
  variable: "--font-geist",
});

const notoSansSC = Noto_Sans_SC({
  display: "swap",
  subsets: ["latin"],
  variable: "--font-noto-sc",
});

export const metadata: Metadata = {
  description: "Lingow 智能同声传译助手交互演示",
  title: "Lingow | 智能同声传译",
};

export const viewport: Viewport = {
  themeColor: "#090909",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      className={`${geist.variable} ${notoSansSC.variable}`}
      lang="zh-CN"
      suppressHydrationWarning
    >
      <body>{children}</body>
    </html>
  );
}
