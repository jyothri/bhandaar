import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";

// The Browse page, through the real router with the backend faked.

const fetchMock = vi.fn<typeof fetch>();

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const google = {
  kind: "google",
  key: "JVVYZFCI0PaW",
  name: "jyo****ri@gmail.com",
  services: {
    drive: {
      granted: true,
      files: 504,
      bytes: 3961548800,
      updated_at: "2026-09-27T10:00:00Z",
    },
    gmail: { granted: false },
    photos: { granted: false },
    gcs: { granted: false },
  },
};
const agent = {
  kind: "agent",
  key: "1",
  name: "seagate1 (optiplex7070)",
  files: 888902,
  bytes: 1696000000000,
  updated_at: "2026-09-27T10:00:00Z",
  physical_drive: 1,
  updating: true,
};

const page = (extra: object) => ({
  path: [],
  folders: [],
  files: [],
  entries: 0,
  page: 1,
  page_size: 200,
  updating: false,
  ...extra,
});

let sources: object[];
let gcsEnabled: boolean;

beforeEach(() => {
  stubEventSource();
  localStorage.clear();
  sources = [google, agent];
  gcsEnabled = false;
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input) => {
    const url = new URL(String(input));
    const folder = url.searchParams.get("folder");
    const n = Number(url.searchParams.get("page"));
    switch (url.pathname) {
      case "/api/auth/me":
        return loggedIn();
      case "/api/settings":
        return json(200, { gcs_enabled: gcsEnabled });
      case "/api/browse/sources":
        return json(200, sources);
      case "/api/browse/google/JVVYZFCI0PaW/drive/children":
        if (folder === "") {
          return json(
            200,
            page({
              folders: [
                {
                  id: "root-id",
                  name: "My Drive",
                  files: 503,
                  bytes: 3961548000,
                },
                {
                  id: "shared-with-me",
                  name: "Shared with me",
                  files: 1,
                  bytes: 800,
                },
              ],
              entries: 2,
            })
          );
        }
        if (folder === "root-id") {
          return json(
            200,
            page({
              path: [{ id: "root-id", name: "My Drive" }],
              folders: [
                {
                  id: "mac-id",
                  name: "from Apple Mac",
                  files: 402,
                  bytes: 3300000000,
                },
              ],
              files: [
                {
                  id: "f1",
                  name: "notes.txt",
                  size: 2048,
                  modified: "2026-09-01T10:00:00Z",
                },
              ],
              entries: 2,
            })
          );
        }
        if (folder === "mac-id") {
          return json(
            200,
            page({
              path: [
                { id: "root-id", name: "My Drive" },
                { id: "mac-id", name: "from Apple Mac" },
              ],
              files: [
                { id: "f2", name: "big.olm", size: 1854486518, modified: null },
              ],
              entries: 1,
            })
          );
        }
        break;
      case "/api/browse/agent/1/children":
        if (n === 1) {
          return json(
            200,
            page({
              folders: [
                { id: "Jyo", name: "Jyo", files: 888900, bytes: 1695999999000 },
              ],
              files: [
                { id: "a.txt", name: "a.txt", size: 10, modified: null },
                {
                  id: "locked",
                  name: "locked",
                  size: null,
                  modified: null,
                  error: "permission denied",
                },
              ],
              entries: 203,
            })
          );
        }
        return json(
          200,
          page({
            files: [
              { id: "last.txt", name: "last.txt", size: 1, modified: null },
            ],
            entries: 203,
            page: 2,
          })
        );
      case "/api/browse/agent/1/status":
        return json(200, {
          last_scan: {
            started_at: "2026-09-27T08:00:00Z",
            finished_at: "2026-09-27T09:00:00Z",
            files_seen: 888902,
            interrupted: false,
          },
          last_synced_at: "2026-09-27T09:05:00Z",
          physical_drive: 1,
          updating: true,
        });
      case "/api/browse/google/JVVYZFCI0PaW/gcs/children":
        if (folder === "") {
          return json(
            200,
            page({
              folders: [
                {
                  id: "jyo-archive/",
                  name: "jyo-archive",
                  files: 3000,
                  bytes: 5000000000,
                  detail: "US-WEST1 · ARCHIVE",
                },
              ],
              entries: 1,
              totals: { files: 3000, bytes: 5000000000 },
              gcs: {
                live_objects: 3000,
                live_bytes: 5000000000,
                noncurrent_objects: 2,
                noncurrent_bytes: 2048,
                soft_deleted_objects: 1,
                soft_deleted_bytes: 1024,
                bytes_by_class: { ARCHIVE: 5000000000 },
              },
            })
          );
        }
        return json(
          200,
          page({
            path: [{ id: "jyo-archive/", name: "jyo-archive" }],
            files: [
              {
                id: "a.txt#2/live",
                name: "a.txt",
                size: 10,
                modified: "2026-09-01T10:00:00Z",
                storage_class: "ARCHIVE",
                state: "live",
              },
              {
                id: "a.txt#1/noncurrent",
                name: "a.txt",
                size: 3,
                modified: null,
                storage_class: "ARCHIVE",
                state: "noncurrent",
              },
            ],
            entries: 2,
            totals: { files: 1, bytes: 10 },
          })
        );
      case "/api/browse/google/JVVYZFCI0PaW/photos/items":
        return json(200, {
          items: [
            {
              media_item_id: "m1",
              media_type: "VIDEO",
              mime_type: "video/mp4",
              filename: "VID_4648.MOV",
              create_time: "2020-10-11T02:08:59Z",
              width: 1920,
              height: 1080,
              camera_make: "",
              camera_model: "",
              focal_length: null,
              f_number: null,
              iso: null,
              exposure_time: "",
              fps: 23.976,
              size: 7908979,
              size_source: "head",
              md5: "",
              scan_id: 12,
            },
            {
              media_item_id: "m2",
              media_type: "PHOTO",
              mime_type: "image/jpeg",
              filename: "PXL_1.jpg",
              create_time: null,
              width: null,
              height: null,
              camera_make: "Google",
              camera_model: "Pixel 8 Pro",
              focal_length: 6.9,
              f_number: 1.68,
              iso: 25,
              exposure_time: "0.0005s",
              fps: null,
              size: null,
              size_source: "unavailable",
              md5: "",
              scan_id: 12,
            },
          ],
          total: 2,
          page: n || 1,
          page_size: 50,
        });
      case "/api/browse/google/JVVYZFCI0PaW/gmail/messages":
        return json(200, {
          messages: [
            {
              message_metadata_id: 1,
              from: "a@example.com",
              subject: "Big one",
              date: "2026-09-01T10:00:00Z",
              size_estimate: 5000000,
              labels: "INBOX,UNREAD",
              scan_id: 5,
            },
          ],
          total: 51,
          page: n,
          page_size: 50,
        });
    }
    return json(404, { error: `unexpected ${url.pathname}` });
  });
  vi.stubGlobal("fetch", fetchMock);
});

function requested(pathname: string): URL[] {
  return fetchMock.mock.calls
    .map(([input]) => new URL(String(input)))
    .filter((u) => u.pathname === pathname);
}

describe("Browse", () => {
  it("is the landing page and first in the nav", async () => {
    renderRoute("/");
    const nav = await screen.findByRole("link", { name: "Browse" });
    expect(nav).toBeInTheDocument();
    expect(await screen.findByRole("combobox")).toHaveValue(
      "google:JVVYZFCI0PaW"
    );
    expect(screen.queryByText("Data about account")).not.toBeInTheDocument();
  });

  it("shows a Drive account's roots, and a folder's contents when expanded", async () => {
    const user = userEvent.setup();
    renderRoute("/?source=google:JVVYZFCI0PaW");
    expect(
      await screen.findByRole("tab", {
        name: /Google Drive\s*504 files · 3.7 GB/,
      })
    ).toHaveAttribute("aria-selected", "true");
    const myDrive = await screen.findByRole("link", { name: "My Drive" });
    // Expanding My Drive loads it, inline.
    await user.click(
      myDrive.closest("summary")!.firstElementChild!.firstElementChild!
    );
    expect(
      await screen.findByRole("link", { name: "notes.txt" })
    ).toHaveAttribute("href", "https://drive.google.com/file/d/f1/view");
    expect(
      screen.getByRole("link", { name: "from Apple Mac" })
    ).toBeInTheDocument();
  });

  it("moves the trail to a folder whose name is clicked", async () => {
    const user = userEvent.setup();
    const { router } = renderRoute(
      "/?source=google:JVVYZFCI0PaW&service=drive&folder=root-id"
    );
    await user.click(
      await screen.findByRole("link", { name: "from Apple Mac" })
    );
    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ folder: "mac-id" })
    );
    const trail = await screen.findByRole("navigation", { name: "Breadcrumb" });
    await waitFor(() =>
      expect(within(trail).getByText("from Apple Mac")).toHaveAttribute(
        "aria-current",
        "page"
      )
    );
    expect(
      within(trail).getByRole("link", { name: "My Drive" })
    ).toBeInTheDocument();
    expect(
      within(trail).getByRole("link", { name: "Google Drive" })
    ).toBeInTheDocument();
    expect(await screen.findByText("big.olm")).toBeInTheDocument();
  });

  it("points to the Request page for a service not granted", async () => {
    const user = userEvent.setup();
    renderRoute("/?source=google:JVVYZFCI0PaW");
    await user.click(await screen.findByRole("tab", { name: /Gmail/ }));
    const link = await screen.findByRole("link", {
      name: /Grant access on the Request page/,
    });
    expect(link).toHaveAttribute(
      "href",
      "/request?type=gmail&account=JVVYZFCI0PaW"
    );
    expect(
      screen.getByRole("tab", { name: /Google Photos.*Not granted/ })
    ).toBeEnabled();
  });

  it("lists an account's messages, largest first, linked to their scans", async () => {
    sources = [
      {
        ...google,
        services: {
          ...google.services,
          gmail: { granted: true, files: 51, bytes: 9000000 },
        },
      },
    ];
    renderRoute("/?source=google:JVVYZFCI0PaW&service=gmail");
    expect(await screen.findByText("Big one")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "5" })).toHaveAttribute(
      "href",
      "/scans/5?page=1"
    );
    expect(screen.getByText("Page 1 of 2")).toBeInTheDocument();
    expect(
      requested(
        "/api/browse/google/JVVYZFCI0PaW/gmail/messages"
      )[0].searchParams.get("sort")
    ).toBe("size");
  });

  it("lists an account's picked photos, largest first, linked to their scans", async () => {
    sources = [
      {
        ...google,
        services: {
          ...google.services,
          photos: { granted: true, files: 2, bytes: 7908979 },
        },
      },
    ];
    renderRoute("/?source=google:JVVYZFCI0PaW&service=photos");
    expect(await screen.findByText("VID_4648.MOV")).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: /Google Photos.*2 items · 7\.5 MB/ })
    ).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("1920 × 1080")).toBeInTheDocument();
    expect(screen.getByText("Google Pixel 8 Pro")).toBeInTheDocument();
    expect(screen.getByText("~7.5 MB")).toBeInTheDocument();
    expect(screen.getByText("Unknown")).toHaveAttribute(
      "title",
      "Google Photos didn't give a size for this item"
    );
    expect(screen.getAllByRole("link", { name: "12" })[0]).toHaveAttribute(
      "href",
      "/scans/12?page=1"
    );
    expect(
      requested(
        "/api/browse/google/JVVYZFCI0PaW/photos/items"
      )[0].searchParams.get("sort")
    ).toBe("size");
  });

  it("points to the Request page for Photos never scanned", async () => {
    sources = [
      {
        ...google,
        services: { ...google.services, photos: { granted: true } },
      },
    ];
    renderRoute("/?source=google:JVVYZFCI0PaW&service=photos");
    expect(
      await screen.findByRole("link", { name: /Scan it on the Request page/ })
    ).toHaveAttribute("href", "/request?type=photos&account=JVVYZFCI0PaW");
  });

  it("has no Cloud Storage tab while it's off in Settings", async () => {
    renderRoute("/?source=google:JVVYZFCI0PaW&service=gcs");
    expect(
      await screen.findByRole("tab", { name: /^Google Drive/, selected: true })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: /^Google Photos/ })
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("tab", { name: /^Google Cloud Storage/ })
    ).toBeNull();
  });

  it("shows an account's Cloud Storage buckets, and what the tree hides", async () => {
    const user = userEvent.setup();
    sources = [
      {
        ...google,
        services: {
          ...google.services,
          gcs: {
            granted: true,
            files: 3000,
            bytes: 5000000000,
            updated_at: "2026-09-28T10:00:00Z",
          },
        },
      },
    ];
    gcsEnabled = true;
    renderRoute("/?source=google:JVVYZFCI0PaW&service=gcs");

    expect(
      await screen.findByRole("tab", {
        name: /Google Cloud Storage\s*3,000 objects · 4.7 GB/,
      })
    ).toHaveAttribute("aria-selected", "true");
    expect(
      await screen.findByRole("link", { name: "jyo-archive" })
    ).toBeInTheDocument();
    expect(screen.getByText("US-WEST1 · ARCHIVE")).toBeInTheDocument();
    expect(screen.getByText("ARCHIVE 4.7 GB")).toBeInTheDocument();
    expect(screen.getByText("2 versions · 2.0 KB")).toBeInTheDocument();
    expect(screen.getByText("1 object · 1.0 KB")).toBeInTheDocument();

    // Into the bucket: both versions of a.txt, the old one marked.
    const bucket = screen.getByRole("link", { name: "jyo-archive" });
    await user.click(
      bucket.closest("summary")!.firstElementChild!.firstElementChild!
    );
    expect(await screen.findByText("noncurrent version")).toBeInTheDocument();
    expect(screen.getAllByText("a.txt")).toHaveLength(2);
    expect(
      requested("/api/browse/google/JVVYZFCI0PaW/gcs/children").map((u) =>
        u.searchParams.get("folder")
      )
    ).toEqual(["", "jyo-archive/"]);
  });

  it("points to the Request page for Cloud Storage not granted", async () => {
    renderRoute("/?source=google:JVVYZFCI0PaW&service=gcs");
    expect(
      await screen.findByRole("link", {
        name: /Grant access on the Request page/,
      })
    ).toHaveAttribute("href", "/request?type=gcs&account=JVVYZFCI0PaW");
  });

  it("shows an agent drive's totals, status and tree, a page at a time", async () => {
    const user = userEvent.setup();
    renderRoute("/?source=agent:1");
    expect(
      await screen.findByText(/^888,902 files · Updated/)
    ).toBeInTheDocument();
    expect(screen.getByText("Updating totals…")).toBeInTheDocument();
    expect(
      await screen.findByText(/^Last scan .* · synced /)
    ).toBeInTheDocument();
    expect(await screen.findByText("permission denied")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /More \(3 left\)/ }));
    expect(await screen.findByText("last.txt")).toBeInTheDocument();
  });

  it("opens on the last source used", async () => {
    localStorage.setItem("browse.source", "agent:1");
    renderRoute("/");
    expect(await screen.findByRole("combobox")).toHaveValue("agent:1");
  });

  it("says where to start when there's nothing to browse", async () => {
    sources = [];
    renderRoute("/");
    expect(
      await screen.findByRole("link", { name: "Link a Google account" })
    ).toHaveAttribute("href", "/request");
  });
});
