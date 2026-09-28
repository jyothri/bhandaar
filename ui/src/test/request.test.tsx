import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";

// The request form, rendered through the real router with the backend faked.

const fetchMock = vi.fn<typeof fetch>();
let scanResponse: () => Promise<Response>;

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  stubEventSource();
  sessionStorage.clear();
  scanResponse = async () => json(200, { scan_id: 42 });
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input));
    if (url.pathname === "/api/auth/me") {
      return loggedIn();
    }
    if (url.pathname === "/api/accounts") {
      return json(200, [
        { clientKey: "k1", displayName: "alice", services: ["gmail"] },
        {
          clientKey: "k2",
          displayName: "bob",
          services: ["gmail", "drive"],
          loginHint: "42",
        },
        {
          clientKey: "k3",
          displayName: "carol",
          services: ["gmail"],
          loginHint: "99",
        },
      ]);
    }
    if (url.pathname === "/api/scans" && init?.method === "POST") {
      return scanResponse();
    }
    return json(404, {});
  });
  vi.stubGlobal("fetch", fetchMock);
});

const scanPosts = () =>
  fetchMock.mock.calls.filter(([, init]) => init?.method === "POST");

async function openForm(path = "/request") {
  const user = userEvent.setup();
  const { router } = renderRoute(path);
  await screen.findByRole("option", { name: "alice" });
  return { user, router };
}

// Catches the page's navigation to Google, and returns its URL.
function stubGoogleRedirect() {
  const assign = vi.fn();
  vi.stubGlobal("location", {
    ...window.location,
    set href(url: string) {
      assign(url);
    },
  });
  return () => new URL(assign.mock.calls[0][0]);
}

const submit = () => screen.getByRole("button", { name: "Submit" });
const filterBox = () => screen.getByLabelText("Query filter");

describe("request form", () => {
  it("builds the filter from the options", async () => {
    const { user } = await openForm();

    await user.click(screen.getByLabelText("Inbox"));
    await user.click(screen.getByLabelText("Unread"));
    await user.type(screen.getByLabelText("Date range"), "2026-09-01");
    await user.type(
      document.getElementById("datepicker-range-end")!,
      "2026-09-10"
    );

    expect(filterBox()).toHaveValue(
      "label:inbox is:unread after:2026/09/01 before:2026/09/11"
    );
  });

  it("labels point at their own inputs", async () => {
    await openForm();

    expect(screen.getByLabelText("Inbox")).toHaveAttribute("id", "inbox");
    expect(screen.getByLabelText("Unread")).toHaveAttribute("id", "unread");
    expect(screen.getByLabelText("Date range")).toHaveAttribute(
      "id",
      "datepicker-range-start"
    );
  });

  it("requires an account", async () => {
    const { user } = await openForm();

    await user.click(screen.getByLabelText("Inbox"));
    await user.click(submit());

    expect(await screen.findByText("Please select an account.")).toBeVisible();
    expect(scanPosts()).toHaveLength(0);
  });

  it("requires a filter", async () => {
    const { user } = await openForm();

    await user.selectOptions(screen.getByLabelText("Accounts"), "k1");
    await user.click(submit());

    expect(
      await screen.findByText("Cannot submit request without any filter.")
    ).toBeVisible();
    expect(scanPosts()).toHaveLength(0);
  });

  it("rejects an end date before the start date", async () => {
    const { user } = await openForm();

    await user.selectOptions(screen.getByLabelText("Accounts"), "k1");
    await user.type(screen.getByLabelText("Date range"), "2026-09-10");
    await user.type(
      document.getElementById("datepicker-range-end")!,
      "2026-09-01"
    );
    await user.click(submit());

    expect(
      await screen.findByText("The end date is before the start date.")
    ).toBeVisible();
    expect(scanPosts()).toHaveLength(0);
  });

  it("submits the scan once, blocking double clicks while pending", async () => {
    let respond!: () => void;
    scanResponse = () =>
      new Promise((resolve) => {
        respond = () => resolve(json(200, { scan_id: 42 }));
      });
    const { user } = await openForm();

    await user.selectOptions(screen.getByLabelText("Accounts"), "k1");
    await user.click(screen.getByLabelText("Unread"));
    await user.click(submit());

    const pending = await screen.findByRole("button", { name: "Submitting…" });
    expect(pending).toBeDisabled();
    await user.click(pending);
    respond();

    expect(
      await screen.findByText("Request submitted successfully. ID: 42")
    ).toBeVisible();
    expect(scanPosts()).toHaveLength(1);
    const [, init] = scanPosts()[0];
    expect(JSON.parse(init?.body as string)).toEqual({
      ScanType: "GMail",
      GMailScan: {
        Filter: "is:unread",
        ClientKey: "k1",
        RefreshToken: "",
      },
    });
  });

  it("shows the backend's error, replacing an earlier success", async () => {
    const { user } = await openForm();
    await user.selectOptions(screen.getByLabelText("Accounts"), "k1");
    await user.click(screen.getByLabelText("Inbox"));
    await user.click(submit());
    await screen.findByText("Request submitted successfully. ID: 42");

    scanResponse = async () =>
      new Response("Failed to start scan: boom\n", {
        status: 500,
        headers: { "Content-Type": "text/plain; charset=utf-8" },
      });
    await user.click(submit());

    expect(
      await screen.findByText(
        "Failed to submit request: Failed to start scan: boom"
      )
    ).toBeVisible();
    await waitFor(() =>
      expect(screen.queryByText(/Request submitted successfully/)).toBeNull()
    );
  });

  it("stores a random OAuth state before sending the user to Google", async () => {
    const googleUrl = stubGoogleRedirect();
    const { user } = await openForm();

    await user.click(
      screen.getByRole("button", { name: "Link another Google account" })
    );

    const url = googleUrl();
    expect(url.origin + url.pathname).toBe(
      "https://accounts.google.com/o/oauth2/v2/auth"
    );
    expect(url.searchParams.get("scope")).toBe(
      "openid email https://www.googleapis.com/auth/gmail.readonly"
    );
    expect(url.searchParams.get("include_granted_scopes")).toBe("true");
    expect(url.searchParams.get("access_type")).toBe("offline");
    expect(url.searchParams.get("client_id")).toBe("test-client-id");
    expect(url.searchParams.get("state")).toBe(
      sessionStorage.getItem("oauthState")
    );
    expect(url.searchParams.has("login_hint")).toBe(false);
    expect(sessionStorage.getItem("oauthLinkService")).toBe("gmail");
  });

  it("selects the account just linked, and drops it from the URL", async () => {
    const { router } = renderRoute("/request?account=k2");

    await waitFor(() =>
      expect(screen.getByLabelText("Accounts")).toHaveValue("k2")
    );
    await waitFor(() =>
      expect(router.state.location.search).toEqual({ type: "gmail" })
    );
  });

  it("ignores an unknown linked account", async () => {
    const { router } = await openForm("/request?account=nope");

    await waitFor(() =>
      expect(router.state.location.search).toEqual({ type: "gmail" })
    );
    expect(screen.getByLabelText("Accounts")).toHaveValue("none");
  });

  it("comes back from linking to the service being linked", async () => {
    sessionStorage.setItem("oauthLinkService", "drive");
    const { router } = await openForm("/request?type=gmail&account=k2");

    await waitFor(() =>
      expect(router.state.location.search).toEqual({ type: "drive" })
    );
    expect(screen.getByRole("tab", { name: "Google Drive" })).toHaveAttribute(
      "aria-selected",
      "true"
    );
    expect(screen.getByLabelText("Accounts")).toHaveValue("k2");
    expect(sessionStorage.getItem("oauthLinkService")).toBeNull();
  });
});

describe("request form, Google Drive", () => {
  const drivePosts = () =>
    scanPosts().map(([, init]) => JSON.parse(init?.body as string));

  async function openDrive(clientKey = "k2") {
    const opened = await openForm("/request?type=drive");
    await opened.user.selectOptions(
      screen.getByLabelText("Accounts"),
      clientKey
    );
    return opened;
  }

  it("switches service, keeping the account, and puts it in the URL", async () => {
    const { user, router } = await openForm();
    await user.selectOptions(screen.getByLabelText("Accounts"), "k2");

    await user.click(screen.getByRole("tab", { name: "Google Drive" }));

    await waitFor(() =>
      expect(router.state.location.search).toEqual({ type: "drive" })
    );
    expect(screen.getByLabelText("Accounts")).toHaveValue("k2");
    expect(screen.getByLabelText("Owned by me")).toBeChecked();
    expect(screen.queryByLabelText("Inbox")).toBeNull();
  });

  it("offers to grant Drive access to an account without it", async () => {
    const googleUrl = stubGoogleRedirect();
    const { user } = await openDrive("k1");

    expect(screen.getByRole("status")).toHaveTextContent(
      "This account hasn't granted Google Drive access."
    );
    expect(screen.queryByLabelText("Owned by me")).toBeNull();
    expect(submit()).toBeDisabled();

    await user.click(
      screen.getByRole("button", { name: "Grant Drive access" })
    );

    const url = googleUrl();
    expect(url.searchParams.get("scope")).toBe(
      "openid email https://www.googleapis.com/auth/drive.metadata.readonly"
    );
    expect(url.searchParams.get("include_granted_scopes")).toBe("true");
    expect(url.searchParams.has("login_hint")).toBe(false); // k1 has none
    expect(sessionStorage.getItem("oauthLinkService")).toBe("drive");
  });

  it("passes the account's login hint when adding a service", async () => {
    const googleUrl = stubGoogleRedirect();
    const { user } = await openDrive("k3");

    await user.click(
      screen.getByRole("button", { name: "Grant Drive access" })
    );

    expect(googleUrl().searchParams.get("login_hint")).toBe("99");
  });

  it("submits the query the fields build", async () => {
    const { user } = await openDrive();

    await user.click(screen.getByLabelText("PDFs"));
    await user.click(screen.getByLabelText("Images"));
    await user.type(screen.getByLabelText("Modified"), "2026-09-01");
    await user.click(submit());

    await screen.findByText("Request submitted successfully. ID: 42");
    expect(drivePosts()).toEqual([
      {
        ScanType: "GDrive",
        GDriveScan: {
          QueryString:
            "mimeType != 'application/vnd.google-apps.folder' and trashed = false and 'me' in owners" +
            " and (mimeType contains 'image/' or mimeType = 'application/pdf')" +
            " and modifiedTime >= '2026-09-01T00:00:00'",
          FolderId: "",
          Recursive: false,
          ClientKey: "k2",
          RefreshToken: "",
        },
      },
    ]);
  });

  it("scans a folder, with its subfolders unless unticked", async () => {
    const { user } = await openDrive();
    const id = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123";

    expect(screen.queryByLabelText("Include subfolders")).toBeNull();
    await user.type(
      screen.getByLabelText("Folder"),
      `https://drive.google.com/drive/folders/${id}?usp=sharing`
    );
    expect(screen.getByLabelText("Include subfolders")).toBeChecked();
    await user.click(submit());
    await screen.findByText("Request submitted successfully. ID: 42");

    await user.click(screen.getByLabelText("Include subfolders"));
    await user.click(submit());
    await waitFor(() => expect(drivePosts()).toHaveLength(2));

    expect(drivePosts().map((p) => p.GDriveScan.FolderId)).toEqual([id, id]);
    expect(drivePosts().map((p) => p.GDriveScan.Recursive)).toEqual([
      true,
      false,
    ]);
  });

  it("rejects a folder that isn't a Drive folder", async () => {
    const { user } = await openDrive();

    await user.type(screen.getByLabelText("Folder"), "https://example.com/x");
    await user.click(submit());

    expect(
      await screen.findByText("That isn't a Google Drive folder link or ID.")
    ).toBeVisible();
    expect(scanPosts()).toHaveLength(0);
  });

  it("sends an edited query instead of the fields'", async () => {
    const { user } = await openDrive();
    const query = () => screen.getByLabelText("Query");

    expect(query()).toBeDisabled();
    await user.click(screen.getByLabelText("Edit query"));
    expect(query()).toBeEnabled();
    expect(query()).toHaveValue(
      "mimeType != 'application/vnd.google-apps.folder' and trashed = false and 'me' in owners"
    );
    expect(screen.getByLabelText("Owned by me")).toBeDisabled();

    await user.clear(query());
    await user.type(query(), "name contains 'tax'");
    await user.click(submit());

    await screen.findByText("Request submitted successfully. ID: 42");
    expect(drivePosts()[0].GDriveScan.QueryString).toBe("name contains 'tax'");
  });

  it("rejects an end date before the start date", async () => {
    const { user } = await openDrive();

    await user.type(screen.getByLabelText("Modified"), "2026-09-10");
    await user.type(screen.getByLabelText("Modified to"), "2026-09-01");
    await user.click(submit());

    expect(
      await screen.findByText("The end date is before the start date.")
    ).toBeVisible();
    expect(scanPosts()).toHaveLength(0);
  });
});
