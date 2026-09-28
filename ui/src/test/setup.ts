import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, vi } from "vitest";
import { activeRouters } from "./routers";

// This file also runs for tests that opt into the node environment
// (// @vitest-environment node), where there is no window or sessionStorage.

// Vitest globals are off, so Testing Library can't register its own cleanup.
afterEach(async () => {
  // Let each router finish the navigation it's loading (see routers.ts).
  // One that hands off to a full page load (the OAuth callback) never
  // does; after a short wait, carry on.
  for (const router of activeRouters) {
    await vi
      .waitFor(
        () => {
          if (router.state.status !== "idle" || router.state.isLoading) {
            throw new Error("router still loading");
          }
        },
        { timeout: 500 }
      )
      .catch(() => {});
  }
  activeRouters.clear();
  cleanup();
  globalThis.sessionStorage?.clear();
});

// jsdom doesn't implement scrolling; the router calls it on navigation.
if (typeof window !== "undefined") {
  window.scrollTo = () => {};
}
