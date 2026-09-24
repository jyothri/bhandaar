import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// This file also runs for tests that opt into the node environment
// (// @vitest-environment node), where there is no window or sessionStorage.

// Vitest globals are off, so Testing Library can't register its own cleanup.
afterEach(() => {
  cleanup();
  globalThis.sessionStorage?.clear();
});

// jsdom doesn't implement scrolling; the router calls it on navigation.
if (typeof window !== "undefined") {
  window.scrollTo = () => {};
}
