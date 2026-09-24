// @vitest-environment node
// No DOM needed; skips starting jsdom for this file.

import { describe, expect, it } from "vitest";
import { formatDateTime, formatDuration } from "./format";

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
