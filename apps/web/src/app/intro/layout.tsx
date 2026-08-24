import type { Metadata } from "next";

export const metadata: Metadata = {
  description: "Lingow 面向面对面交流的实时同传与 AI 语音助手。",
  title: "Lingow | 面对面实时同传",
};

export default function IntroLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return children;
}
