const basePath = process.env.NEXT_PUBLIC_BASE_PATH?.replace(/\/$/, "") ?? "";

export const siteNavItems = [
  { href: "/intro", label: "首页" },
  { href: "/intro/product", label: "产品" },
  { href: "/intro/docs", label: "文档" },
  { href: "/intro/about", label: "关于 Lingow" },
] as const;

export function currentSiteNavItem(pathname: string | null) {
  const normalizedPathname = pathname?.replace(/\/+$/, "") ?? "";
  return siteNavItems.find(
    (item) => normalizedPathname === item.href || normalizedPathname.endsWith(item.href),
  ) ?? siteNavItems[0];
}

export function siteHref(path: string): string {
  if (path === "/") return `${basePath}/`;
  return `${basePath}${path}`;
}
