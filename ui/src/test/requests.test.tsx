import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";

// Request History, rendered through the real router with the backend faked.
// Two of the accounts mask to the same name.

const fetchMock = vi.fn<typeof fetch>();

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const scan = (scan_id: number, name: string) => ({
  scan_id,
  name,
  scan_type: "gmail",
  search_filter: "is:unread",
  search_path: "",
  scan_start_time: "2026-09-27T10:00:00Z",
  scan_duration_in_sec: "12.5",
  status: "Completed",
});

beforeEach(() => {
  stubEventSource();
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input) => {
    const url = new URL(String(input));
    switch (url.pathname) {
      case "/api/auth/me":
        return loggedIn();
      case "/api/scans/accounts":
        return json(200, [
          { clientKey: "aB3xyz", displayName: "jyo****ri@gmail.com" },
          { clientKey: "Qw9abc", displayName: "jyo****ri@gmail.com" },
          { clientKey: "Zz1def", displayName: "oth****er@gmail.com" },
        ]);
      case "/api/scans/requests/aB3xyz":
        return json(200, [scan(5, "jyo****ri@gmail.com")]);
      case "/api/scans/requests/Qw9abc":
        return json(200, [scan(9, "jyo****ri@gmail.com")]);
    }
    return json(404, {});
  });
  vi.stubGlobal("fetch", fetchMock);
});

describe("request history", () => {
  it("tells apart accounts that share a name", async () => {
    renderRoute("/requests");

    expect(
      await screen.findByRole("option", { name: "jyo****ri@gmail.com · aB3x" })
    ).toHaveValue("aB3xyz");
    expect(
      screen.getByRole("option", { name: "jyo****ri@gmail.com · Qw9a" })
    ).toHaveValue("Qw9abc");
    expect(
      screen.getByRole("option", { name: "oth****er@gmail.com" })
    ).toHaveValue("Zz1def");
  });

  it("lists the scans of the account selected, by its client key", async () => {
    renderRoute("/requests");
    const user = userEvent.setup();
    await screen.findByRole("option", { name: "jyo****ri@gmail.com · Qw9a" });

    await user.selectOptions(
      screen.getByLabelText("Select an account"),
      "Qw9abc"
    );

    const link = await screen.findByRole("link", { name: "9" });
    expect(link).toHaveAttribute("href", "/scans/9?page=1");
    expect(screen.queryByRole("link", { name: "5" })).toBeNull();
    expect(screen.getByRole("cell", { name: "Gmail" })).toBeVisible();
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([input]) =>
            new URL(String(input)).pathname === "/api/scans/requests/Qw9abc"
        )
      ).toBe(true)
    );
  });

  it("keeps the selected account in the URL and the trail", async () => {
    const { router } = renderRoute("/requests");
    const user = userEvent.setup();
    const trail = await screen.findByRole("navigation", { name: "Breadcrumb" });
    expect(within(trail).getByText("Request History")).toHaveAttribute(
      "aria-current",
      "page"
    );
    await screen.findByRole("option", { name: "oth****er@gmail.com" });

    await user.selectOptions(
      screen.getByLabelText("Select an account"),
      "Qw9abc"
    );

    await waitFor(() =>
      expect(router.state.location.search).toEqual({ account: "Qw9abc" })
    );
    expect(within(trail).getByText("jyo****ri@gmail.com · Qw9a")).toHaveAttribute(
      "aria-current",
      "page"
    );
    expect(
      within(trail).getByRole("link", { name: "Request History" })
    ).toHaveAttribute("href", "/requests");
  });

  it("opens with the account in the URL selected", async () => {
    renderRoute("/requests?account=aB3xyz");

    expect(await screen.findByRole("link", { name: "5" })).toBeVisible();
    expect(screen.getByLabelText("Select an account")).toHaveValue("aB3xyz");
  });
});
