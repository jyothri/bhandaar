import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderRoute, stubEventSource } from "./renderRoute";

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
  scanResponse = async () => json(200, { scan_id: 42 });
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input));
    if (url.pathname === "/api/accounts") {
      return json(200, [{ clientKey: "k1", displayName: "alice" }]);
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

async function openForm() {
  const user = userEvent.setup();
  renderRoute("/request");
  await screen.findByRole("option", { name: "alice" });
  return user;
}

const submit = () => screen.getByRole("button", { name: "Submit" });
const filterBox = () => screen.getByLabelText("Query filter");

describe("request form", () => {
  it("builds the filter from the options", async () => {
    const user = await openForm();

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
    const user = await openForm();

    await user.click(screen.getByLabelText("Inbox"));
    await user.click(submit());

    expect(await screen.findByText("Please select an account.")).toBeVisible();
    expect(scanPosts()).toHaveLength(0);
  });

  it("requires a filter", async () => {
    const user = await openForm();

    await user.selectOptions(screen.getByLabelText("Accounts"), "k1");
    await user.click(submit());

    expect(
      await screen.findByText("Cannot submit request without any filter.")
    ).toBeVisible();
    expect(scanPosts()).toHaveLength(0);
  });

  it("rejects an end date before the start date", async () => {
    const user = await openForm();

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
    const user = await openForm();

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
        Username: "alice",
      },
    });
  });

  it("shows the backend's error, replacing an earlier success", async () => {
    const user = await openForm();
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
    const assign = vi.fn();
    vi.stubGlobal("location", {
      ...window.location,
      set href(url: string) {
        assign(url);
      },
    });
    const user = await openForm();

    await user.click(
      screen.getByRole("button", { name: "Continue with Google" })
    );

    const url = new URL(assign.mock.calls[0][0]);
    expect(url.origin + url.pathname).toBe(
      "https://accounts.google.com/o/oauth2/v2/auth"
    );
    expect(url.searchParams.get("scope")).toBe(
      "https://www.googleapis.com/auth/gmail.readonly"
    );
    expect(url.searchParams.get("client_id")).toBe("test-client-id");
    expect(url.searchParams.get("state")).toBe(
      sessionStorage.getItem("oauthState")
    );
  });
});
