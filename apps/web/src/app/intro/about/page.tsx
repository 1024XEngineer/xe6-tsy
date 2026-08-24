import type { Metadata } from "next";

import { AboutPage } from "../subpage";

export const metadata: Metadata = { title: "关于 Lingow", description: "了解 Lingow 的产品定位、当前阶段与开源项目。" };

export default function Page() { return <AboutPage />; }
