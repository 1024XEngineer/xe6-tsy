import type { Metadata } from "next";

import { DocumentationPage } from "../subpage";

export const metadata: Metadata = { title: "文档 | Lingow", description: "Lingow 开发者文档入口。" };

export default function Page() { return <DocumentationPage />; }
