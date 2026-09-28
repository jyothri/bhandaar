// @vitest-environment node

import { describe, expect, it } from "vitest";
import { safeRedirect } from "./safeRedirect";

describe("safeRedirect", () => {
  it.each([
    ["/requests", "/requests"],
    ["/request?x=1#y", "/request?x=1#y"],
    ["//evil.example.com/x", "/"],
    ["https://evil.example.com", "/"],
    ["javascript:alert(1)", "/"],
    ["", "/"],
  ])("%s → %s", (target, want) => {
    expect(safeRedirect(target)).toBe(want);
  });
});
