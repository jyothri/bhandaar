import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { loggedIn, renderRoute, stubEventSource } from "./renderRoute";

// The Request page's Google Cloud Storage tab, with the backend faked. See
// docs/archive/gcs-scans.md, "Request page".

const fetchMock = vi.fn<typeof fetch>();

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

let projects: () => Response;

beforeEach(() => {
  stubEventSource();
  sessionStorage.clear();
  projects = () =>
    json(200, [
      { projectId: "backup-276614", displayName: "personal-backup" },
      { projectId: "home-automation-396715", displayName: "home-automation" },
    ]);
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
          {
            clientKey: "k5",
            displayName: "erin",
            services: ["gcs"],
            canListProjects: true,
          },
          { clientKey: "k6", displayName: "frank", services: ["gcs"] },
        ]);
      case "GET /api/gcs/k5/projects":
        return projects();
      case "GET /api/gcs/k5/projects/backup-276614/buckets":
      case "GET /api/gcs/k6/projects/typed-project-1/buckets":
        return json(200, [
          {
            name: "jyo-archive",
            location: "US-WEST1",
            storageClass: "ARCHIVE",
            versioning: false,
            softDeleteDays: 7,
            requesterPays: false,
          },
          {
            name: "shared-data",
            location: "US",
            storageClass: "STANDARD",
            versioning: true,
            requesterPays: true,
          },
        ]);
      case "POST /api/scans":
        return json(200, { scan_id: 42 });
    }
    return json(404, {});
  });
  vi.stubGlobal("fetch", fetchMock);
});

async function openGcs(account?: string) {
  const user = userEvent.setup();
  renderRoute("/request?type=gcs");
  await screen.findByRole("option", { name: "erin" });
  if (account) {
    await user.selectOptions(screen.getByLabelText("Accounts"), account);
  }
  return user;
}

const posted = () => {
  const call = fetchMock.mock.calls.find(([, init]) => init?.method === "POST");
  return call ? JSON.parse(String(call[1]!.body)) : undefined;
};

const submit = () => screen.getByRole("button", { name: "Submit" });

describe("request form, Google Cloud Storage", () => {
  it("has a tab, and asks an account without it to grant access", async () => {
    const user = await openGcs("k1");

    expect(
      screen.getByRole("tab", { name: "Google Cloud Storage" })
    ).toHaveAttribute("aria-selected", "true");
    expect(
      screen.getByText(
        "This account hasn't granted Google Cloud Storage access."
      )
    ).toBeVisible();

    const assign = vi.fn();
    vi.stubGlobal("location", {
      ...window.location,
      set href(url: string) {
        assign(url);
      },
    });
    await user.click(
      screen.getByRole("button", { name: "Grant Cloud Storage access" })
    );
    const scope = new URL(assign.mock.calls[0][0]).searchParams.get("scope");
    expect(scope).toContain(
      "https://www.googleapis.com/auth/devstorage.read_only"
    );
    expect(scope).toContain(
      "https://www.googleapis.com/auth/cloudplatformprojects.readonly"
    );
  });

  it("scans a listed project's buckets", async () => {
    const user = await openGcs("k5");

    await user.selectOptions(
      await screen.findByLabelText("Project"),
      "backup-276614"
    );
    expect(
      await screen.findByRole("option", { name: "All buckets (2)" })
    ).toBeInTheDocument();
    expect(
      screen.getByRole("option", {
        name: "shared-data · US · STANDARD · versioned · Requester Pays: skipped",
      })
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Prefix")).toBeDisabled();
    await user.click(submit());

    await waitFor(() =>
      expect(posted()).toEqual({
        ScanType: "GStorage",
        GStorageScan: {
          ClientKey: "k5",
          ProjectId: "backup-276614",
          Bucket: "",
          Prefix: "",
          Versions: true,
          SoftDeleted: true,
        },
      })
    );
    expect(await screen.findByText(/Request submitted/)).toBeVisible();
  });

  it("scans one bucket under a prefix", async () => {
    const user = await openGcs("k5");

    await user.selectOptions(
      await screen.findByLabelText("Project"),
      "backup-276614"
    );
    await user.selectOptions(
      await screen.findByLabelText("Buckets"),
      "jyo-archive"
    );
    await user.type(screen.getByLabelText("Prefix"), "backups/2024/");
    await user.click(screen.getByLabelText("Soft-deleted objects"));
    await user.click(submit());

    await waitFor(() =>
      expect(posted()?.GStorageScan).toEqual({
        ClientKey: "k5",
        ProjectId: "backup-276614",
        Bucket: "jyo-archive",
        Prefix: "backups/2024/",
        Versions: true,
        SoftDeleted: false,
      })
    );
  });

  it("takes a typed project ID when the account can't list its projects", async () => {
    const user = await openGcs("k6");

    expect(
      screen.getByText(
        "This account didn't allow listing its projects: type a project ID."
      )
    ).toBeVisible();
    await user.type(screen.getByLabelText("Project"), "typed-project-1");
    expect(
      await screen.findByRole("option", { name: "All buckets (2)" })
    ).toBeInTheDocument();
    await user.click(submit());

    await waitFor(() =>
      expect(posted()?.GStorageScan.ProjectId).toBe("typed-project-1")
    );
  });

  it("falls back to typing when the projects can't be listed", async () => {
    projects = () =>
      new Response("Google refused to list the account's projects.", {
        status: 502,
        headers: { "Content-Type": "text/plain" },
      });
    await openGcs("k5");

    expect(
      await screen.findByText(
        /Couldn't list the projects \(Google refused to list the account's projects\.\): type a project ID\./
      )
    ).toBeVisible();
    expect(screen.getByLabelText("Project")).toHaveAttribute("type", "text");
  });

  it("requires a project", async () => {
    const user = await openGcs("k6");

    await user.type(screen.getByLabelText("Project"), "no");
    await user.click(submit());

    expect(
      await screen.findByText("Pick a project, or type its ID.")
    ).toBeVisible();
    expect(posted()).toBeUndefined();
  });
});
