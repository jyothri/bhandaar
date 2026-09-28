import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";
import { PhotosPick } from "../types/photos";

// The Request page's Google Photos tab, with the backend faked. See
// docs/specs/photos-picker.md, "UI".

const fetchMock = vi.fn<typeof fetch>();

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const pick = (changes: Partial<PhotosPick> = {}): PhotosPick => ({
  sessionKey: "s1",
  pickerUri: "https://photos.google.com/picker/abc",
  state: "waiting",
  pickBy: "2026-09-28T12:30:00Z",
  ...changes,
});

// What the fake backend answers.
let active: PhotosPick | null;
let started: () => Response;
let byKey: () => PhotosPick;

beforeEach(() => {
  stubEventSource();
  sessionStorage.clear();
  active = null;
  started = () => json(200, pick());
  byKey = () => pick();
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input));
    const method = init?.method ?? "GET";
    switch (`${method} ${url.pathname}`) {
      case "GET /api/auth/me":
        return loggedIn();
      case "GET /api/accounts":
        return json(200, [
          { clientKey: "k1", displayName: "alice", services: ["gmail"] },
          { clientKey: "k4", displayName: "dave", services: ["photos"] },
        ]);
      case "GET /api/photos/sessions":
        return json(200, active);
      case "POST /api/photos/sessions":
        return started();
      case "GET /api/photos/sessions/s1":
        return json(200, byKey());
      case "DELETE /api/photos/sessions/s1":
        byKey = () => pick({ state: "cancelled" });
        return new Response(null, { status: 204 });
    }
    return json(404, {});
  });
  vi.stubGlobal("fetch", fetchMock);
});

async function openPhotos() {
  const user = userEvent.setup();
  renderRoute("/request?type=photos");
  await screen.findByRole("option", { name: "dave" });
  return user;
}

// A window.open that returns a fake popup, or null when blocked.
function stubPopup(blocked = false) {
  const popup = {
    opener: {} as unknown,
    location: { href: "" },
    close: vi.fn(),
  };
  const open = vi.fn(() => (blocked ? null : popup));
  vi.stubGlobal("open", open);
  return { open, popup };
}

const pickButton = () =>
  screen.getByRole("button", { name: "Pick in Google Photos" });

describe("request form, Google Photos", () => {
  it("has a Photos tab without the Submit button", async () => {
    await openPhotos();

    expect(screen.getByRole("tab", { name: "Google Photos" })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    expect(pickButton()).toBeVisible();
    expect(screen.queryByRole("button", { name: "Submit" })).toBeNull();
    expect(
      screen.getByText(/Sizes are of the copy Google Photos keeps/)
    ).toBeVisible();
  });

  it("offers to grant Photos access to an account without it", async () => {
    const user = await openPhotos();

    await user.selectOptions(screen.getByLabelText("Accounts"), "k1");

    expect(
      screen.getByText("This account hasn't granted Google Photos access.")
    ).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Grant Photos access" })
    ).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Pick in Google Photos" })
    ).toBeNull();
  });

  it("requires an account", async () => {
    const user = await openPhotos();
    const { open } = stubPopup();

    await user.click(pickButton());

    expect(screen.getByText("Please select an account.")).toBeVisible();
    expect(open).not.toHaveBeenCalled();
  });

  it("opens Google Photos to pick, then waits", async () => {
    const user = await openPhotos();
    const { open, popup } = stubPopup();

    await user.selectOptions(screen.getByLabelText("Accounts"), "k4");
    await user.click(pickButton());

    expect(
      await screen.findByText(/Waiting for you to pick in Google Photos/)
    ).toBeVisible();
    expect(open).toHaveBeenCalledWith("", "_blank");
    expect(popup.location.href).toBe(
      "https://photos.google.com/picker/abc/autoclose"
    );
    expect(popup.opener).toBeNull();
    const post = fetchMock.mock.calls.find(
      ([, init]) => init?.method === "POST"
    );
    expect(JSON.parse(String(post![1]!.body))).toEqual({ clientKey: "k4" });
    expect(
      screen.getByRole("link", { name: "Open Google Photos" })
    ).toHaveAttribute("href", "https://photos.google.com/picker/abc/autoclose");
  });

  it("says so when the browser blocks the window", async () => {
    const user = await openPhotos();
    stubPopup(true);

    await user.selectOptions(screen.getByLabelText("Accounts"), "k4");
    await user.click(pickButton());

    expect(
      await screen.findByText(/Your browser blocked the Google Photos window/)
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: "Open Google Photos" })
    ).toBeVisible();
  });

  it("shows the backend's error, and closes the window", async () => {
    started = () =>
      new Response("A Google Photos pick is already in progress.", {
        status: 409,
        headers: { "Content-Type": "text/plain" },
      });
    const user = await openPhotos();
    const { popup } = stubPopup();

    await user.selectOptions(screen.getByLabelText("Accounts"), "k4");
    await user.click(pickButton());

    expect(
      await screen.findByText(
        "Failed to start picking: A Google Photos pick is already in progress."
      )
    ).toBeVisible();
    expect(popup.close).toHaveBeenCalled();
  });

  it("cancels a pick", async () => {
    const user = await openPhotos();
    stubPopup();

    await user.selectOptions(screen.getByLabelText("Accounts"), "k4");
    await user.click(pickButton());
    await user.click(await screen.findByRole("button", { name: "Cancel" }));

    expect(await screen.findByText("Pick cancelled.")).toBeVisible();
    expect(
      fetchMock.mock.calls.some(([, init]) => init?.method === "DELETE")
    ).toBe(true);
    expect(pickButton()).toBeVisible();
  });

  it("picks up a pick already under way", async () => {
    active = pick({ state: "scanning", scanId: 7 });
    byKey = () => pick({ state: "scanning", scanId: 7 });
    await openPhotos();

    expect(
      await screen.findByText(/Picked. Scanning what you picked/)
    ).toBeVisible();
    expect(screen.getByRole("link", { name: "View results" })).toHaveAttribute(
      "href",
      "/scans/7?page=1"
    );
  });

  it("links to the scan once it's done", async () => {
    active = pick({ state: "scanning", scanId: 7 });
    byKey = () => pick({ state: "done", scanId: 7 });
    await openPhotos();

    expect(await screen.findByText(/Scan 7 is done/)).toBeVisible();
    expect(screen.getByRole("link", { name: "View results" })).toBeVisible();
    await waitFor(() => expect(pickButton()).toBeEnabled());
  });

  it("says when nothing was picked in time", async () => {
    active = pick();
    byKey = () => pick({ state: "expired" });
    await openPhotos();

    expect(
      await screen.findByText(
        "Nothing was picked in time. Pick again when you're ready."
      )
    ).toBeVisible();
  });
});
