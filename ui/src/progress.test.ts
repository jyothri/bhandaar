// @vitest-environment node
// No DOM needed; skips starting jsdom for this file.

import { describe, expect, it } from "vitest";
import { describeScanError, mergeProgress } from "./progress";
import { Progress } from "./types/scans";

const running = (scan_id: number, processed: number): Progress => ({
  client_key: "k1",
  processed_count: processed,
  active_count: 2,
  completion_pct: 0,
  elapsed_in_sec: 12,
  eta_in_sec: 0,
  scan_id,
  status: "Running",
});

// A scan's final event: status and error, zero counts.
const final = (
  scan_id: number,
  status: "Completed" | "Failed",
  error?: string
): Progress => ({
  client_key: "",
  processed_count: 0,
  active_count: 0,
  completion_pct: 0,
  elapsed_in_sec: 0,
  eta_in_sec: 0,
  scan_id,
  status,
  error,
});

describe("mergeProgress", () => {
  it("takes the first event, and each newer Running event", () => {
    const first = mergeProgress(null, running(4, 1));
    expect(first).toEqual(running(4, 1));
    expect(mergeProgress(first, running(4, 5))).toEqual(running(4, 5));
  });

  it("keeps the counts when the final event arrives", () => {
    const row = mergeProgress(running(4, 5), final(4, "Failed", "boom"));
    expect(row).toMatchObject({
      scan_id: 4,
      processed_count: 5,
      elapsed_in_sec: 12,
      status: "Failed",
      error: "boom",
    });
  });

  it("ignores a Running event that arrives after the scan ended", () => {
    const ended = mergeProgress(running(4, 5), final(4, "Completed"));
    expect(mergeProgress(ended, running(4, 5))).toBe(ended);
  });

  it("shows a final event on its own when no progress came before", () => {
    expect(mergeProgress(null, final(4, "Failed", "boom"))).toEqual(
      final(4, "Failed", "boom")
    );
  });

  it("switches to a new scan", () => {
    const ended = mergeProgress(running(4, 5), final(4, "Completed"));
    expect(mergeProgress(ended, running(5, 0))).toEqual(running(5, 0));
  });
});

describe("describeScanError", () => {
  it("turns invalid_grant into a hint to link the account again", () => {
    const googleError =
      'failed to list messages: auth: cannot fetch token: 400\nResponse: {\n  "error": "invalid_grant"\n}';
    expect(describeScanError(googleError)).toMatch(/Link the account again/);
  });

  it("keeps only the first line, and shortens long ones", () => {
    expect(describeScanError("first line\nsecond line")).toBe("first line");
    const long = describeScanError("x".repeat(500));
    expect(long).toHaveLength(200);
    expect(long.endsWith("…")).toBe(true);
  });

  it("handles a missing error", () => {
    expect(describeScanError(undefined)).toBe("Unknown error");
  });
});
