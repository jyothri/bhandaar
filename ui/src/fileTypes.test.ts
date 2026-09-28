// @vitest-environment node

import { describe, expect, it } from "vitest";
import { fileKind } from "./fileTypes";

describe("fileKind", () => {
  it.each([
    ["x", "application/vnd.google-apps.folder", "folder"],
    ["IMG_1.HEIC", "image/heic", "image"],
    ["clip", "video/quicktime", "video"],
    ["Budget", "application/vnd.google-apps.spreadsheet", "sheet"],
    [
      "deck.pptx",
      "application/vnd.openxmlformats-officedocument.presentationml.presentation",
      "slides",
    ],
    ["Notes", "application/vnd.google-apps.document", "document"],
    ["a.zip", "application/zip", "archive"],
    ["data", "application/json", "code"],
    // An unknown MIME type falls back to the name.
    ["song.mp3", "application/octet-stream", "audio"],
    ["blob", "application/octet-stream", "file"],
  ])("%s (%s) is %s", (name, mime, kind) => {
    expect(fileKind(name, mime)).toBe(kind);
  });

  it.each([
    ["IMG_0001.JPG", "image"],
    ["movie.m2ts", "video"],
    ["report.pdf", "document"],
    ["backup.tar.gz", "archive"],
    ["main.go", "code"],
    [".bashrc", "file"],
    ["README", "file"],
    ["weird.xyz", "file"],
  ])("%s is %s by its extension", (name, kind) => {
    expect(fileKind(name)).toBe(kind);
  });
});
