const basePath = process.env.NEXT_PUBLIC_BASE_PATH?.replace(/\/$/, "") ?? "";

export function siteHref(path: string): string {
  if (path === "/") return `${basePath}/`;
  return `${basePath}${path}`;
}
