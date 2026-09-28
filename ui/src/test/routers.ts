import type { AnyRouter } from "@tanstack/react-router";

// Routers renderRoute made in the running test. The setup's afterEach
// waits for each to finish loading before cleanup: a navigation still
// loading when a test file's environment is torn down would otherwise
// set state after window is gone, an unhandled error that fails the run.
export const activeRouters = new Set<AnyRouter>();
