import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";

// The Duplicates page, through the real router with the backend faked.

const fetchMock = vi.fn<typeof fetch>();

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const s1 = "agent:1";
const s2 = "agent:2";
const drive = "google:k1:drive";

const summary = (extra = {}) => ({
  reclaimable: 5 * 1024 ** 3,
  groups: { file: 2, folder: 1, photo: 0 },
  by_source: [
    {
      source: s2,
      label: "seagate2 (optiplex)",
      bytes: 3 * 1024 ** 3,
      files: 2,
    },
    { source: drive, label: "al***ce · Google Drive", bytes: 1024, files: 1 },
  ],
  uncomparable: [
    {
      source: drive,
      label: "al***ce · Google Drive",
      folders: 4,
      reason: "They hold Google Docs, Sheets or Slides, which have no MD5.",
    },
  ],
  built_at: new Date(Date.now() - 4 * 60_000).toISOString(),
  updating: false,
  took_ms: 1000,
  ...extra,
});

const member = (source: string, path: string, extra = {}) => ({
  source,
  label: source === drive ? "al***ce · Google Drive" : `drive ${source}`,
  item: path,
  path,
  folder: path.replace(/\/?[^/]*$/, ""),
  size: 2 * 1024 ** 3,
  files: 1,
  modified: "2026-09-01T10:00:00Z",
  physical_drive: null,
  ...extra,
});

const groupsPage = {
  groups: [
    {
      id: 11,
      kind: "file",
      name: "big.iso",
      size: 2 * 1024 ** 3,
      files: 1,
      copies: 12,
      reclaimable: 22 * 1024 ** 3,
      same_physical: false,
      sources: [s1, s2],
      match: "",
      members: Array.from({ length: 10 }, (_, i) =>
        member(i % 2 ? s1 : s2, `iso/big${i}.iso`)
      ),
    },
    {
      id: 12,
      kind: "file",
      name: "same.bin",
      size: 50,
      files: 1,
      copies: 2,
      reclaimable: 0,
      same_physical: true,
      sources: [s1, s2],
      match: "",
      members: [
        member(drive, "My Drive/same.bin", { item: "fileid1", folder: "A" }),
      ],
    },
  ],
  labels: { [s1]: "seagate1 (optiplex)", [s2]: "seagate2 (optiplex)" },
  total: 51,
  page: 1,
  page_size: 50,
};

let summaryReply: object;
let groupQueries: URLSearchParams[];

beforeEach(() => {
  stubEventSource();
  summaryReply = summary();
  groupQueries = [];
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input) => {
    const url = new URL(String(input));
    if (url.pathname === "/api/auth/me") {
      return loggedIn();
    }
    if (url.pathname === "/api/duplicates/summary") {
      return json(200, summaryReply);
    }
    if (url.pathname === "/api/duplicates/groups") {
      groupQueries.push(url.searchParams);
      return json(200, groupsPage);
    }
    if (url.pathname === "/api/duplicates/groups/11/members") {
      return json(200, {
        members: Array.from({ length: 12 }, (_, i) =>
          member(s1, `all/big${i}.iso`)
        ),
        total: 12,
        page: 1,
        page_size: 200,
      });
    }
    return json(404, {});
  });
  vi.stubGlobal("fetch", fetchMock);
});

describe("Duplicates", () => {
  it("shows the summary, the kinds and a page of groups", async () => {
    renderRoute("/duplicates");
    expect(await screen.findByText("5.0 GB")).toBeInTheDocument();
    expect(screen.getByText(/3 groups · Updated 4m ago/)).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: /seagate2 \(optiplex\) 3\.0 GB/ })
    ).toHaveAttribute("href", `/duplicates?source=${encodeURIComponent(s2)}`);
    const tabs = screen.getByRole("tablist", { name: "Kind" });
    expect(within(tabs).getByRole("tab", { name: /^Files/ })).toHaveAttribute(
      "aria-selected",
      "true"
    );

    expect(await screen.findByText("big.iso")).toBeInTheDocument();
    expect(screen.getByText("2.0 GB × 12")).toBeInTheDocument();
    expect(screen.getByText("22.0 GB reclaimable")).toBeInTheDocument();
    expect(screen.getByText("Same physical drive")).toBeInTheDocument();
    expect(screen.getByText("Page 1 of 2")).toBeInTheDocument();
    expect(groupQueries[0].toString()).toBe("kind=file");
  });

  it("lists a group's copies, with links, and loads the rest", async () => {
    renderRoute("/duplicates");
    await userEvent.click(
      await screen.findByRole("button", { name: /same\.bin/ })
    );
    const copy = screen.getByText("My Drive/same.bin").closest("li")!;
    expect(within(copy).getByRole("link", { name: "Browse" })).toHaveAttribute(
      "href",
      "/?source=google%3Ak1&service=drive&folder=A"
    );
    expect(within(copy).getByRole("link", { name: /Drive/ })).toHaveAttribute(
      "href",
      "https://drive.google.com/file/d/fileid1/view"
    );

    await userEvent.click(screen.getByRole("button", { name: /big\.iso/ }));
    expect(screen.getByText("iso/big0.iso")).toBeInTheDocument();
    await userEvent.click(
      screen.getByRole("button", { name: "Show 2 more copies" })
    );
    expect(await screen.findByText("all/big11.iso")).toBeInTheDocument();
    expect(screen.queryByText("iso/big0.iso")).not.toBeInTheDocument();
  });

  it("filters, starting again at page 1", async () => {
    const { router } = renderRoute("/duplicates?page=2");
    await screen.findByText("big.iso");
    await userEvent.click(screen.getByLabelText("Hide same physical drive"));
    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        hide_same_physical: true,
      })
    );
    await userEvent.selectOptions(screen.getByLabelText("Source"), s2);
    await userEvent.selectOptions(
      screen.getByLabelText("Minimum size"),
      String(100 << 20)
    );
    await waitFor(() =>
      expect(groupQueries[groupQueries.length - 1]?.toString()).toBe(
        `kind=file&source=${encodeURIComponent(s2)}&min_size=104857600&hide_same_physical=1`
      )
    );
  });

  it("lists the folders that couldn't be compared", async () => {
    renderRoute("/duplicates?kind=folder");
    await userEvent.click(
      await screen.findByText("4 folders couldn't be compared")
    );
    expect(
      screen.getByText(/They hold Google Docs, Sheets or Slides/)
    ).toBeInTheDocument();
    expect(groupQueries[0].get("kind")).toBe("folder");
  });

  it("says so while the first build runs", async () => {
    summaryReply = summary({ built_at: null, updating: true });
    renderRoute("/duplicates");
    expect(
      await screen.findByText(/Finding your duplicates/)
    ).toBeInTheDocument();
    expect(groupQueries).toHaveLength(0);
  });
});
