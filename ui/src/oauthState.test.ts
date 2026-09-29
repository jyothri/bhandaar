import { describe, expect, it } from "vitest";
import {
  clearLinkService,
  consumeOAuthState,
  createOAuthState,
  linkService,
  rememberLinkService,
} from "./oauthState";

describe("OAuth state", () => {
  it("creates a fresh random value each time", () => {
    const first = createOAuthState();
    const second = createOAuthState();
    expect(first).toMatch(/^[0-9a-f-]{36}$/);
    expect(second).not.toBe(first);
  });

  it("accepts the value this tab created, only once", () => {
    const state = createOAuthState();
    expect(consumeOAuthState(state)).toBe(true);
    expect(consumeOAuthState(state)).toBe(false);
  });

  it("rejects a different value, and clears the stored one", () => {
    const state = createOAuthState();
    expect(consumeOAuthState("forged")).toBe(false);
    expect(consumeOAuthState(state)).toBe(false);
  });

  it("rejects everything when no link was started", () => {
    expect(consumeOAuthState("")).toBe(false);
    expect(consumeOAuthState("anything")).toBe(false);
  });
});

describe("link service", () => {
  it("is remembered until cleared", () => {
    expect(linkService()).toBeNull();
    rememberLinkService("drive");
    expect(linkService()).toBe("drive");
    expect(linkService()).toBe("drive");
    clearLinkService();
    expect(linkService()).toBeNull();
  });

  it("knows Photos", () => {
    rememberLinkService("photos");
    expect(linkService()).toBe("photos");
  });

  it("ignores anything that isn't a service", () => {
    sessionStorage.setItem("oauthLinkService", "calendar");
    expect(linkService()).toBeNull();
  });
});
