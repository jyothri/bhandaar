import { screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderRoute, stubEventSource } from "./renderRoute";

// The OAuth callback must leave the SPA with a full page load: /api/glink is
// served by the backend, not by a client route. The test config sets the
// backend to http://backend.test.

const replace = vi.fn();

// Stubs the page's origin as if the UI were served from `origin`. The router
// reads window.origin, the callback builds redirectUri from
// window.location.origin, and full page loads go through location.replace.
function serveUiFrom(origin: string) {
  vi.stubGlobal("origin", origin);
  vi.stubGlobal("location", {
    ...window.location,
    origin,
    href: `${origin}/oauth/glink`,
    protocol: new URL(origin).protocol,
    host: new URL(origin).host,
    replace,
  });
}

beforeEach(() => {
  replace.mockReset();
  stubEventSource();
  sessionStorage.setItem("oauthState", "s1");
});

describe("OAuth callback", () => {
  it.each([
    ["the same origin as the backend (production)", "http://backend.test"],
    ["a different origin from the backend (dev)", "http://ui.test"],
  ])(
    "forwards the code to the backend with a full page load when the UI is on %s",
    async (_, origin) => {
      serveUiFrom(origin);

      renderRoute("/oauth/glink?code=4%2F0A&state=s1&scope=x");

      await waitFor(() => expect(replace).toHaveBeenCalledTimes(1));
      const url = new URL(replace.mock.calls[0][0], origin);
      expect(url.origin + url.pathname).toBe("http://backend.test/api/glink");
      expect(url.searchParams.get("code")).toBe("4/0A");
      expect(url.searchParams.get("redirectUri")).toBe(`${origin}/oauth/glink`);
    }
  );

  it("doesn't forward a code whose state doesn't match", async () => {
    serveUiFrom("http://backend.test");

    renderRoute("/oauth/glink?code=abc&state=forged");

    expect(
      await screen.findByText(/doesn't match this browser session/)
    ).toBeVisible();
    expect(replace).not.toHaveBeenCalled();
  });

  it("says linking was cancelled when consent is declined", async () => {
    serveUiFrom("http://backend.test");

    renderRoute("/oauth/glink?error=access_denied&state=s1");

    expect(await screen.findByText("Linking was cancelled.")).toBeVisible();
    expect(replace).not.toHaveBeenCalled();
  });
});
