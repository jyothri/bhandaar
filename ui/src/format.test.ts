// @vitest-environment node
// No DOM needed; skips starting jsdom for this file.

import { describe, expect, it } from "vitest";
import {
  formatAgo,
  formatBytes,
  formatDateTime,
  formatDuration,
  shareOf,
  scanTypeLabel,
} from "./format";

describe("formatDuration", () => {
  it.each([
    [0, "0:00"],
    [2.19, "0:02"],
    [59.6, "1:00"],
    [65, "1:05"],
    [3599, "59:59"],
    [3600, "1:00:00"],
    [3725, "1:02:05"],
    [-1, "0:00"],
  ])("formats %s seconds as %s", (seconds, expected) => {
    expect(formatDuration(seconds)).toBe(expected);
  });
});

describe("formatDateTime", () => {
  it("formats the same instant the same way, whatever its offset", () => {
    // The history API used to send Pacific wall time labelled as UTC; real
    // instants must format identically regardless of the offset they carry.
    expect(formatDateTime("2026-09-24T12:52:01-04:00")).toBe(
      formatDateTime("2026-09-24T16:52:01Z")
    );
  });

  it("formats in the viewer's locale and time zone", () => {
    const iso = "2026-09-24T16:52:01Z";
    const expected = new Intl.DateTimeFormat(undefined, {
      dateStyle: "medium",
      timeStyle: "short",
    }).format(new Date(iso));
    expect(formatDateTime(iso)).toBe(expected);
  });

  it("returns unparseable input unchanged", () => {
    expect(formatDateTime("not a date")).toBe("not a date");
  });
});

describe("formatBytes", () => {
  it.each([
    [0, "0 B"],
    [1023, "1023 B"],
    [1536, "1.5 KB"],
    [6996060, "6.7 MB"],
    [3961548800, "3.7 GB"],
    [150 * 1024 ** 3, "150 GB"],
  ])("formats %d as %s", (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want);
  });
});

describe("scanTypeLabel", () => {
  it("names the scan types, and passes others through", () => {
    expect(scanTypeLabel("gmail")).toBe("Gmail");
    expect(scanTypeLabel("google_drive")).toBe("Google Drive");
    expect(scanTypeLabel("something_new")).toBe("something_new");
  });
});

describe("formatAgo", () => {
  const now = Date.parse("2026-09-28T12:00:00Z");
  it.each([
    ["2026-09-28T11:59:30Z", "just now"],
    ["2026-09-28T11:55:00Z", "5m ago"],
    ["2026-09-28T10:00:00Z", "2h ago"],
    ["2026-09-25T12:00:00Z", "3d ago"],
    ["not a time", "not a time"],
  ])("%s is %s", (iso, want) => {
    expect(formatAgo(iso, now)).toBe(want);
  });
});

describe("shareOf", () => {
  it.each([
    [50, 200, 25],
    [200, 200, 100],
    // A sliver for a tiny share, nothing for nothing.
    [1, 1_000_000, 0.5],
    [0, 200, 0],
    // A folder of 0 bytes has no shares.
    [0, 0, 0],
    [10, 0, 0],
  ])("%d of %d is %d%%", (bytes, total, want) => {
    expect(shareOf(bytes, total)).toBe(want);
  });
});
