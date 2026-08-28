import { describe, expect, it } from "vitest";

import { currentSiteNavItem } from "./site-paths";

describe("currentSiteNavItem", () => {
  it("matches static-export routes with a trailing slash and base path", () => {
    expect(currentSiteNavItem("/intro/docs/").label).toBe("文档");
    expect(currentSiteNavItem("/xe6-tsy/intro/docs/").label).toBe("文档");
  });

  it("falls back to the home entry for an unknown pathname", () => {
    expect(currentSiteNavItem("/unknown/").label).toBe("首页");
  });
});
