import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";

// A scan's results view, through the real router with the backend faked.

const fetchMock = vi.fn<typeof fetch>();

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const summary = (scan_id: number, scan_type: string, extra = {}) => ({
  scan_id,
  scan_type,
  name: "jyo****ri@gmail.com",
  client_key: "JVVYZFCI0PaW",
  search_path: "",
  search_filter: "trashed = false",
  status: "Completed",
  scan_start_time: "2026-09-27T10:00:00Z",
  scan_duration_in_sec: "46.2",
  item_count: 0,
  total_bytes: 0,
  folder_count: 0,
  ...extra,
});

const driveRow = (id: number, path: string, extra = {}) => ({
  scan_data_id: id,
  name: path.slice(path.lastIndexOf("/") + 1),
  path,
  size: 2048,
  modified: "2026-09-01T10:00:00Z",
  md5: "0123456789abcdef",
  is_dir: false,
  file_count: 1,
  file_id: `f${id}`,
  ...extra,
});

beforeEach(() => {
  stubEventSource();
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input) => {
    const url = new URL(String(input));
    const page = Number(url.searchParams.get("page"));
    switch (url.pathname) {
      case "/api/auth/me":
        return loggedIn();
      case "/api/scans/accounts":
        return json(200, [
          { clientKey: "JVVYZFCI0PaW", displayName: "jyo****ri@gmail.com" },
        ]);
      case "/api/scans/11/summary":
        return json(
          200,
          summary(11, "google_drive", {
            search_path: "My Drive/from Apple Mac (0B8F) and subfolders",
            item_count: 12,
            total_bytes: 3961548800,
            folder_count: 1,
          })
        );
      case "/api/scans/11":
        return json(200, {
          pagination_info: { size: 13, page },
          scan_data:
            page === 1
              ? [
                  driveRow(1, "My Drive/from Apple Mac/Desktop", {
                    is_dir: true,
                    file_count: 5,
                    size: 6996060,
                    md5: "",
                    file_id: "folder1",
                  }),
                  driveRow(2, "My Drive/from Apple Mac/Desktop/q1.pdf"),
                ]
              : [driveRow(13, "My Drive/from Apple Mac/z.txt")],
        });
      case "/api/scans/6/summary":
        return json(
          200,
          summary(6, "gmail", { item_count: 1, total_bytes: 2048 })
        );
      case "/api/gmaildata/6":
        return json(200, {
          pagination_info: { size: 1, page },
          message_metadata: [
            {
              message_metadata_id: 1,
              from: "news@example.com",
              to: "me",
              subject: "Weekly digest",
              date: "2026-09-02T08:00:00Z",
              size_estimate: 2048,
            },
          ],
        });
      case "/api/scans/3/summary":
        return json(200, summary(3, "photos", { client_key: "", name: "" }));
      case "/api/scans/9/summary":
        return json(404, {});
    }
    return json(404, {});
  });
  vi.stubGlobal("fetch", fetchMock);
});

describe("scan results", () => {
  it("shows a Drive scan's summary and files, linked to Drive", async () => {
    renderRoute("/scans/11");

    expect(
      await screen.findByRole("heading", { name: "Scan 11: Google Drive" })
    ).toBeVisible();
    expect(
      screen.getByText("My Drive/from Apple Mac (0B8F) and subfolders")
    ).toBeVisible();
    expect(screen.getByText("12 files, 3.7 GB, 1 folder")).toBeVisible();

    const folder = await screen.findByRole("link", { name: "Desktop/" });
    expect(folder).toHaveAttribute(
      "href",
      "https://drive.google.com/drive/folders/folder1"
    );
    const file = screen.getByRole("link", { name: "q1.pdf" });
    expect(file).toHaveAttribute(
      "href",
      "https://drive.google.com/file/d/f2/view"
    );
    const fileRow = file.closest("tr")!;
    expect(
      within(fileRow).getByText("My Drive/from Apple Mac/Desktop")
    ).toBeVisible();
    expect(within(fileRow).getByText("2.0 KB")).toBeVisible();
    expect(within(fileRow).getByText("01234567")).toHaveAttribute(
      "title",
      "0123456789abcdef"
    );
    // A folder shows how many files are under it.
    expect(within(folder.closest("tr")!).getByText("5")).toBeVisible();
  });

  it("pages through the results", async () => {
    const { router } = renderRoute("/scans/11");
    const user = userEvent.setup();

    await user.click(await screen.findByRole("link", { name: "Next" }));

    expect(await screen.findByRole("link", { name: "z.txt" })).toBeVisible();
    expect(screen.getByText("Page 2 of 2")).toBeVisible();
    expect(router.state.location.search).toEqual({ page: 2 });
    expect(screen.queryByRole("link", { name: "Next" })).toBeNull();
  });

  it("shows a Gmail scan's new messages", async () => {
    renderRoute("/scans/6");

    expect(await screen.findByText("1 new message, 2.0 KB")).toBeVisible();
    expect(await screen.findByText("Weekly digest")).toBeVisible();
    expect(screen.getByText("news@example.com")).toBeVisible();
  });

  it("says when a scan type has no results view", async () => {
    renderRoute("/scans/3");

    expect(
      await screen.findByText("No results view for this scan type yet.")
    ).toBeVisible();
  });

  it("reports a scan it can't load", async () => {
    renderRoute("/scans/9");

    await waitFor(() =>
      expect(screen.getByText(/Couldn't load scan 9/)).toBeVisible()
    );
  });

  it("shows where the scan lives, with links back up", async () => {
    const { router } = renderRoute("/scans/11");
    const user = userEvent.setup();
    const trail = await screen.findByRole("navigation", { name: "Breadcrumb" });

    const account = await within(trail).findByRole("link", {
      name: "jyo****ri@gmail.com",
    });
    expect(account).toHaveAttribute("href", "/requests?account=JVVYZFCI0PaW");
    expect(
      within(trail).getByRole("link", { name: "Request History" })
    ).toHaveAttribute("href", "/requests");
    expect(within(trail).getByText("Scan 11")).toHaveAttribute(
      "aria-current",
      "page"
    );
    // The Request History tab stays highlighted.
    const nav = screen.getByRole("navigation", { name: "Main" });
    expect(
      within(nav).getByRole("link", { name: "Request History" })
    ).toHaveAttribute("aria-current", "page");

    await user.click(account);
    await waitFor(() =>
      expect(router.state.location.pathname).toBe("/requests")
    );
    expect(router.state.location.search).toEqual({ account: "JVVYZFCI0PaW" });
  });

  it("leaves the account out of a scan that has none", async () => {
    renderRoute("/scans/3");
    const trail = await screen.findByRole("navigation", { name: "Breadcrumb" });

    await screen.findByText("No results view for this scan type yet.");
    // The separators are hidden from screen readers.
    expect(
      within(trail)
        .getAllByRole("listitem")
        .map((li) => li.textContent)
    ).toEqual(["Request History", "Scan 3"]);
  });
});
