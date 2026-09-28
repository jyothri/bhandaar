import { describe, expect, it } from "vitest";
import { accountLabels } from "./accountLabels";

describe("accountLabels", () => {
  it("uses the display name alone when it's unique", () => {
    const labels = accountLabels([
      { clientKey: "aB3xyz", displayName: "jyo****ri@gmail.com" },
      { clientKey: "Qw9abc", displayName: "oth****er@gmail.com" },
    ]);
    expect(labels.get("aB3xyz")).toBe("jyo****ri@gmail.com");
    expect(labels.get("Qw9abc")).toBe("oth****er@gmail.com");
  });

  it("adds the start of the client key to names two accounts share", () => {
    const labels = accountLabels([
      { clientKey: "aB3xyz", displayName: "jyo****ri@gmail.com" },
      { clientKey: "Qw9abc", displayName: "jyo****ri@gmail.com" },
      { clientKey: "Zz1def", displayName: "oth****er@gmail.com" },
    ]);
    expect(labels.get("aB3xyz")).toBe("jyo****ri@gmail.com · aB3x");
    expect(labels.get("Qw9abc")).toBe("jyo****ri@gmail.com · Qw9a");
    expect(labels.get("Zz1def")).toBe("oth****er@gmail.com");
  });
});
