// @vitest-environment node
// No DOM needed; skips starting jsdom for this file.

import { describe, expect, it, vi } from "vitest";
import { buildGmailFilter, dateForApi } from "./gmailFilter";

const none = { inbox: false, unread: false, startDate: "", endDate: "" };

describe("buildGmailFilter", () => {
  it("is empty with no options", () => {
    expect(buildGmailFilter(none)).toBe("");
  });

  it("joins every option with single spaces, no trailing space", () => {
    expect(
      buildGmailFilter({
        inbox: true,
        unread: true,
        startDate: "2026-09-01",
        endDate: "2026-09-10",
      })
    ).toBe("label:inbox is:unread after:2026/09/01 before:2026/09/11");
  });

  it("uses the documented is:unread", () => {
    expect(buildGmailFilter({ ...none, unread: true })).toBe("is:unread");
  });

  it("includes the end date, since Gmail's before: is exclusive", () => {
    expect(buildGmailFilter({ ...none, endDate: "2026-09-10" })).toBe(
      "before:2026/09/11"
    );
  });
});

describe("dateForApi", () => {
  it("formats YYYY-MM-DD as YYYY/MM/DD", () => {
    expect(dateForApi("2026-09-24")).toBe("2026/09/24");
  });

  it.each([
    ["2026-12-31", "2027/01/01"],
    ["2024-02-28", "2024/02/29"],
    ["2025-02-28", "2025/03/01"],
    ["2026-03-08", "2026/03/09"], // US DST starts
  ])(
    "adds a day to %s across month, year and DST boundaries",
    (input, next) => {
      expect(dateForApi(input, 1)).toBe(next);
    }
  );

  it.each(["Asia/Kolkata", "America/Los_Angeles", "Pacific/Kiritimati"])(
    "doesn't shift the date in %s",
    (tz) => {
      // Node applies a changed TZ immediately; unstubEnvs restores it.
      vi.stubEnv("TZ", tz);
      expect(new Date("2026-09-24T00:00:00Z").getTimezoneOffset()).not.toBe(0);
      expect(dateForApi("2026-09-24")).toBe("2026/09/24");
      expect(dateForApi("2026-09-24", 1)).toBe("2026/09/25");
    }
  );
});
