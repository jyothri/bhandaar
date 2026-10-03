import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";

// The Manage data page and its deletions, through the real router with the
// backend faked.

const fetchMock = vi.fn<typeof fetch>();

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const account = (extra = {}) => ({
  client_key: "JVVYZFCI0PaW",
  label: "jyo****ri@gmail.com",
  services: ["gmail", "drive"],
  recorded: {
    gmail: { files: 5744, bytes: 516703723 },
    drive: { files: 504, bytes: 3961682114 },
    gcs: {},
    photos: {},
  },
  service_scans: { gmail: 4, drive: 2 },
  scans: 9,
  job: null,
  ...extra,
});

const drive = (id: number, hostname: string, extra = {}) => ({
  id,
  drive_id: "seagate1",
  hostname,
  files: 29,
  bytes: 625183198,
  last_synced_at: "2026-09-27T22:22:55Z",
  physical_drive: 1,
  other_copies: [],
  job: null,
  ...extra,
});

let settings: object;
let deletion: object;
let deleteAnswer: Response | Promise<Response> | null;

beforeEach(() => {
  stubEventSource();
  settings = {
    accounts: [account()],
    drives: [
      drive(5, "JyothriingasMBP.attlocal.net", {
        other_copies: ["optiplex7070"],
      }),
      drive(1, "optiplex7070", { files: 888902 }),
    ],
  };
  deletion = {};
  deleteAnswer = null;
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input));
    if (url.pathname === "/api/auth/me") {
      return loggedIn();
    }
    if (url.pathname === "/api/manage-data") {
      return json(200, settings);
    }
    if (init?.method === "DELETE") {
      return deleteAnswer ?? json(202, { id: 42, status: "running" });
    }
    if (url.pathname === "/api/deletions/42") {
      return json(200, { id: 42, status: "done", counts: {}, ...deletion });
    }
    return json(404, { error: `unexpected ${url.pathname}` });
  });
  vi.stubGlobal("fetch", fetchMock);
});

function deletes(): [string, RequestInit | undefined][] {
  return fetchMock.mock.calls
    .filter(([, init]) => init?.method === "DELETE")
    .map(([input, init]) => [new URL(String(input)).pathname, init]);
}

describe("Manage data", () => {
  it("is a nav tab", async () => {
    renderRoute("/manage-data");
    const nav = await screen.findByRole("navigation", { name: "Main" });
    expect(
      within(nav).getByRole("link", { name: "Manage data" })
    ).toHaveAttribute("aria-current", "page");
  });

  it("lists accounts, and drives by the box they came from", async () => {
    renderRoute("/manage-data");
    expect(await screen.findByText("jyo****ri@gmail.com")).toBeInTheDocument();
    expect(screen.getByText(/5,744 messages/)).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "JyothriingasMBP.attlocal.net" })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "optiplex7070" })
    ).toBeInTheDocument();
    expect(
      screen.getByText("Also uploaded from optiplex7070")
    ).toBeInTheDocument();
  });

  it("deletes a drive from one box after confirming", async () => {
    const user = userEvent.setup();
    deletion = {
      kind: "agent_drive",
      label: "seagate1 (JyothriingasMBP.attlocal.net)",
      counts: { files: 29 },
    };
    renderRoute("/manage-data");
    await user.click(
      (await screen.findAllByRole("button", { name: "Delete…" }))[0]
    );

    const dialog = screen.getByRole("dialog", {
      name: "Delete seagate1 from JyothriingasMBP.attlocal.net?",
    });
    // Cancel is focused, so Enter alone never deletes.
    expect(
      within(dialog).getByRole("button", { name: "Cancel" })
    ).toHaveFocus();
    expect(dialog).toHaveTextContent(
      "nor its copies uploaded from optiplex7070"
    );
    expect(dialog).toHaveTextContent("the next sync uploads all of it");

    await user.click(
      within(dialog).getByRole("button", { name: "Delete drive" })
    );
    expect(deletes().map(([path]) => path)).toEqual(["/api/agent-drives/5"]);
    expect(
      await screen.findByText(
        "Deleted seagate1 (JyothriingasMBP.attlocal.net): 29 files."
      )
    ).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("closes a dialog on Escape without deleting", async () => {
    const user = userEvent.setup();
    renderRoute("/manage-data");
    await user.click(
      await screen.findByRole("button", { name: "Delete Gmail data" })
    );
    expect(screen.getByRole("dialog")).toHaveTextContent(
      "5,744 messages and 4 Gmail scans"
    );
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(deletes()).toEqual([]);
  });

  it("deletes Gmail data", async () => {
    const user = userEvent.setup();
    deletion = {
      kind: "gmail",
      label: "jyo****ri@gmail.com · Gmail",
      counts: { messages: 5744, scans: 4 },
    };
    renderRoute("/manage-data");
    await user.click(
      await screen.findByRole("button", { name: "Delete Gmail data" })
    );
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Delete Gmail data",
      })
    );
    expect(deletes().map(([path]) => path)).toEqual([
      "/api/accounts/JVVYZFCI0PaW/gmail",
    ]);
    expect(
      await screen.findByText(
        "Deleted jyo****ri@gmail.com · Gmail: 5,744 messages, 4 scans."
      )
    ).toBeInTheDocument();
  });

  it("offers to delete only the services with data", async () => {
    renderRoute("/manage-data");
    expect(
      await screen.findByRole("button", { name: "Delete Gmail data" })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Delete Google Drive data" })
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Delete Cloud Storage data" })
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Delete Google Photos data" })
    ).toBeNull();
  });

  it("deletes one service's data, keeping the account", async () => {
    const user = userEvent.setup();
    deletion = {
      kind: "drive",
      label: "jyo****ri@gmail.com · Google Drive",
      counts: { drive_items: 504, scans: 2 },
    };
    renderRoute("/manage-data");
    await user.click(
      await screen.findByRole("button", { name: "Delete Google Drive data" })
    );
    const dialog = screen.getByRole("dialog", {
      name: "Delete Google Drive data for jyo****ri@gmail.com?",
    });
    expect(dialog).toHaveTextContent(
      "This deletes 504 files and 2 Google Drive scans from Bhandaar."
    );
    expect(dialog).toHaveTextContent("the account stays connected");
    await user.click(
      within(dialog).getByRole("button", { name: "Delete Google Drive data" })
    );
    expect(deletes().map(([path]) => path)).toEqual([
      "/api/accounts/JVVYZFCI0PaW/drive",
    ]);
    expect(
      await screen.findByText(
        "Deleted jyo****ri@gmail.com · Google Drive: 504 items, 2 scans."
      )
    ).toBeInTheDocument();
  });

  it("disconnects an account only once its name is typed exactly", async () => {
    const user = userEvent.setup();
    deletion = {
      kind: "account",
      label: "jyo****ri@gmail.com",
      counts: { scans: 9, messages: 5744, drive_items: 504, gcs_objects: 0 },
      revoke: "failed: Google answered 500 backend",
    };
    renderRoute("/manage-data");
    await user.click(
      await screen.findByRole("button", { name: "Disconnect account" })
    );
    const dialog = screen.getByRole("dialog", {
      name: "Disconnect jyo****ri@gmail.com?",
    });
    const confirm = within(dialog).getByRole("button", {
      name: "Disconnect account",
    });
    const field = within(dialog).getByLabelText(/To confirm, type/);
    expect(confirm).toBeDisabled();

    await user.type(field, "jyo****ri@gmail");
    await user.keyboard("{Enter}");
    expect(confirm).toBeDisabled();
    expect(deletes()).toEqual([]);

    await user.type(field, ".com");
    expect(confirm).toBeEnabled();
    await user.keyboard("{Enter}");
    expect(deletes().map(([path, init]) => [path, init?.body])).toEqual([
      [
        "/api/accounts/JVVYZFCI0PaW",
        JSON.stringify({ confirm: "jyo****ri@gmail.com" }),
      ],
    ]);
    // Google couldn't revoke it: say what to do.
    expect(
      await screen.findByText(/couldn't revoke its access at Google/)
    ).toHaveTextContent("myaccount.google.com/permissions");
  });

  it("can't be closed while its request is out", async () => {
    const user = userEvent.setup();
    let answer: (r: Response) => void = () => {};
    deleteAnswer = new Promise((resolve) => (answer = resolve));
    renderRoute("/manage-data");
    await user.click(
      await screen.findByRole("button", { name: "Delete Gmail data" })
    );
    const dialog = screen.getByRole("dialog");
    await user.click(
      within(dialog).getByRole("button", { name: "Delete Gmail data" })
    );
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Cancel" })
    ).toBeDisabled();

    answer(
      new Response("Another deletion of this account is running.", {
        status: 409,
        headers: { "Content-Type": "text/plain" },
      })
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Another deletion of this account is running."
    );
    // Once answered, it closes again.
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("shows the server's refusal in the dialog", async () => {
    const user = userEvent.setup();
    deleteAnswer = new Response(
      "Scan 12 of this account is running. Wait for it to finish, then try again.",
      {
        status: 409,
        headers: { "Content-Type": "text/plain" },
      }
    );
    renderRoute("/manage-data");
    await user.click(
      await screen.findByRole("button", { name: "Delete Gmail data" })
    );
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Delete Gmail data",
      })
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Scan 12 of this account is running"
    );
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("disables an account's deletions while one of its scans runs, and shows one in progress", async () => {
    settings = {
      accounts: [account({ running_scan: 12 })],
      drives: [drive(1, "optiplex7070", { job: { id: 7, status: "running" } })],
    };
    renderRoute("/manage-data");
    expect(await screen.findByText(/Scan 12 is running/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Disconnect account" })
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Delete Gmail data" })
    ).toBeDisabled();
    expect(screen.getByText("Deleting…")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete…" })).toBeNull();
  });

  it("names the page", async () => {
    renderRoute("/manage-data");
    await waitFor(() => expect(document.title).toBe("Manage data · Bhandaar"));
  });
});
