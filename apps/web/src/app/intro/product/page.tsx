import type { Metadata } from "next";

import { ProductPage } from "../subpage";

export const metadata: Metadata = { title: "产品 | Lingow", description: "了解 Lingow 的面对面同传与 AI 语音助手。" };

export default function Page() { return <ProductPage />; }
