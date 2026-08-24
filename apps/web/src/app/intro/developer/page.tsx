import type { Metadata } from "next";

import { DeveloperPage } from "../subpage";

export const metadata: Metadata = { title: "开发者 | Lingow", description: "了解 Lingow 的实时协议、架构与设备控制核心。" };

export default function Page() { return <DeveloperPage />; }
