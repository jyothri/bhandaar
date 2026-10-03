import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";

// The Settings page, from the user menu, with the backend faked.

const fetchMock = vi.fn<typeof fetch>();

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

let saved: { gcs_enabled: boolean };
let putAnswer: (body: string) => Response;

beforeEach(() => {
  stubEventSource();
  saved = { gcs_enabled: false };
  putAnswer = (body) => {
    saved = JSON.parse(body);
    return json(200, saved);
  };
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input));
    const method = init?.method ?? "GET";
    switch (`${method} ${url.pathname}`) {
      case "GET /api/auth/me":
        return loggedIn();
      case "GET /api/settings":
        return json(200, saved);
      case "PUT /api/settings":
        return putAnswer(String(init!.body));
      case "GET /api/accounts":
        return json(200, []);
    }
    return json(404, {});
  });
  vi.stubGlobal("fetch", fetchMock);
});

describe("Settings", () => {
  it("opens from the user menu, before Log out", async () => {
    const user = userEvent.setup();
    const { router } = renderRoute("/request");
    await user.click(
      await screen.findByRole("button", { name: "Signed in as alice" })
    );
    const items = screen.getAllByRole("menuitem");
    expect(items.map((i) => i.textContent)).toEqual(["Settings", "Log out"]);
    await user.click(items[0]);
    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/settings")
    );
    expect(
      await screen.findByRole("heading", { name: "Settings" })
    ).toBeInTheDocument();
    expect(document.title).toBe("Settings · Bhandaar");
  });

  it("turns Cloud Storage on and off, off by default", async () => {
    const user = userEvent.setup();
    renderRoute("/settings");
    const gcs = await screen.findByRole("switch");
    expect(gcs).toHaveAttribute("aria-checked", "false");

    await user.click(gcs);
    await waitFor(() => expect(saved).toEqual({ gcs_enabled: true }));
    await waitFor(() => expect(gcs).toHaveAttribute("aria-checked", "true"));

    // The Request page now has the tab.
    await user.click(screen.getByRole("link", { name: "Request" }));
    expect(
      await screen.findByRole("tab", { name: "Google Cloud Storage" })
    ).toBeInTheDocument();
  });

  it("says when a change couldn't be saved", async () => {
    const user = userEvent.setup();
    putAnswer = () =>
      new Response("Failed to save your settings", { status: 500 });
    renderRoute("/settings");
    await user.click(await screen.findByRole("switch"));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Couldn't save: Failed to save your settings"
    );
    expect(screen.getByRole("switch")).toHaveAttribute("aria-checked", "false");
  });
});
