import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Vitest globals are off, so Testing Library can't register its own cleanup.
afterEach(() => {
  cleanup();
  sessionStorage.clear();
});

// jsdom doesn't implement scrolling; the router calls it on navigation.
window.scrollTo = () => {};
