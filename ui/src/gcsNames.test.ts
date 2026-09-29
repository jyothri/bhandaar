import { describe, expect, it } from "vitest";
import { validProjectId } from "./gcsNames";

describe("validProjectId", () => {
  it("takes project IDs, with or without a domain", () => {
    expect(validProjectId("personal-backup-276614")).toBe(true);
    expect(validProjectId("example.com:my-project")).toBe(true);
  });

  it("rejects anything else", () => {
    for (const id of ["abc", "Upper-case1", "ends-with-", "1digit-first", ""]) {
      expect(validProjectId(id)).toBe(false);
    }
  });
});
