import { describe, expect, it } from "vitest";
import { buildDriveQuery, driveFolderId, DriveQueryOptions } from "./driveQuery";

const defaults: DriveQueryOptions = {
  ownedByMe: true,
  includeTrash: false,
  fileTypes: [],
  startDate: "",
  endDate: "",
};

const NOT_FOLDER = "mimeType != 'application/vnd.google-apps.folder'";

describe("buildDriveQuery", () => {
  it("skips folders and trash, and keeps your own files, by default", () => {
    expect(buildDriveQuery(defaults)).toBe(
      `${NOT_FOLDER} and trashed = false and 'me' in owners`
    );
  });

  it("drops the trash and owner terms when asked", () => {
    expect(
      buildDriveQuery({ ...defaults, ownedByMe: false, includeTrash: true })
    ).toBe(NOT_FOLDER);
  });

  it("adds one file type as is", () => {
    expect(buildDriveQuery({ ...defaults, fileTypes: ["pdfs"] })).toBe(
      `${NOT_FOLDER} and trashed = false and 'me' in owners and mimeType = 'application/pdf'`
    );
  });

  it("ORs several file types, in the form's order", () => {
    expect(
      buildDriveQuery({
        ...defaults,
        ownedByMe: false,
        fileTypes: ["googleDocs", "images", "videos"],
      })
    ).toBe(
      `${NOT_FOLDER} and trashed = false and (mimeType contains 'image/' or mimeType contains 'video/' or mimeType contains 'application/vnd.google-apps.')`
    );
  });

  it("includes the end date by comparing with the next day, in UTC", () => {
    expect(
      buildDriveQuery({
        ...defaults,
        ownedByMe: false,
        startDate: "2026-02-01",
        endDate: "2026-02-28",
      })
    ).toBe(
      `${NOT_FOLDER} and trashed = false and modifiedTime >= '2026-02-01T00:00:00' and modifiedTime < '2026-03-01T00:00:00'`
    );
  });
});

describe("driveFolderId", () => {
  const id = "1AbCdEfGhIjKlMnOpQrStUvWxYz0123";

  it.each([
    [id, id],
    [`  ${id} `, id],
    [`https://drive.google.com/drive/folders/${id}`, id],
    [`https://drive.google.com/drive/folders/${id}?usp=sharing`, id],
    [`https://drive.google.com/drive/u/1/folders/${id}`, id],
    [`https://drive.google.com/open?id=${id}`, id],
    ["", ""],
  ])("reads %j as %j", (input, want) => {
    expect(driveFolderId(input)).toBe(want);
  });

  it.each([
    "short",
    "x' or name != 'y",
    `https://example.com/drive/folders/${id}`,
    "https://drive.google.com/drive/my-drive",
    "https://drive.google.com/drive/folders/bad'id",
  ])("rejects %j", (input) => {
    expect(driveFolderId(input)).toBeNull();
  });
});
