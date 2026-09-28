// Which icon a file gets: by MIME type for Drive, by extension otherwise.
// See docs/specs/ui-refresh.md, "File-type icons".

export type FileKind =
  | "folder"
  | "image"
  | "video"
  | "audio"
  | "document"
  | "sheet"
  | "slides"
  | "archive"
  | "code"
  | "file";

const FOLDER = "application/vnd.google-apps.folder";

// MIME types by kind, exact or as a prefix ending in "/" or ".".
const mimeKinds: [FileKind, string[]][] = [
  [
    "image",
    [
      "image/",
      "application/vnd.google-apps.photo",
      "application/vnd.google-apps.drawing",
    ],
  ],
  ["video", ["video/", "application/vnd.google-apps.video"]],
  ["audio", ["audio/", "application/vnd.google-apps.audio"]],
  [
    "sheet",
    [
      "application/vnd.google-apps.spreadsheet",
      "application/vnd.ms-excel",
      "application/vnd.openxmlformats-officedocument.spreadsheetml.",
      "text/csv",
    ],
  ],
  [
    "slides",
    [
      "application/vnd.google-apps.presentation",
      "application/vnd.ms-powerpoint",
      "application/vnd.openxmlformats-officedocument.presentationml.",
    ],
  ],
  [
    "document",
    [
      "application/pdf",
      "application/vnd.google-apps.document",
      "application/msword",
      "application/vnd.openxmlformats-officedocument.wordprocessingml.",
      "application/rtf",
      "text/plain",
      "text/markdown",
    ],
  ],
  [
    "archive",
    [
      "application/zip",
      "application/x-zip-compressed",
      "application/x-tar",
      "application/gzip",
      "application/x-gzip",
      "application/x-7z-compressed",
      "application/vnd.rar",
      "application/x-rar-compressed",
      "application/x-apple-diskimage",
      "application/x-iso9660-image",
    ],
  ],
  [
    "code",
    [
      "application/json",
      "text/html",
      "text/css",
      "text/javascript",
      "application/javascript",
      "application/xml",
      "text/xml",
      "text/x-python",
      "application/x-sh",
    ],
  ],
];

const extensionKinds: Record<string, FileKind> = {};
for (const [kind, extensions] of [
  [
    "image",
    "jpg jpeg png gif heic heif webp tif tiff bmp svg raw cr2 nef arw dng",
  ],
  ["video", "mp4 mov m4v avi mkv m2ts mts wmv webm 3gp"],
  ["audio", "mp3 m4a wav flac aac ogg wma aiff"],
  ["document", "pdf doc docx txt rtf pages md odt"],
  ["sheet", "xls xlsx csv numbers ods"],
  ["slides", "ppt pptx key odp"],
  ["archive", "zip tar gz tgz bz2 xz 7z rar dmg iso"],
  [
    "code",
    "json html htm css js jsx ts tsx py go java xml sh yaml yml c h cpp rs",
  ],
] as const) {
  for (const extension of extensions.split(" ")) {
    extensionKinds[extension] = kind;
  }
}

/** The kind of a file, from its Drive MIME type if any, else its name. */
export function fileKind(name: string, mimeType?: string): FileKind {
  if (mimeType) {
    if (mimeType === FOLDER) {
      return "folder";
    }
    for (const [kind, types] of mimeKinds) {
      if (
        types.some((t) =>
          /[/.]$/.test(t) ? mimeType.startsWith(t) : mimeType === t
        )
      ) {
        return kind;
      }
    }
  }
  const dot = name.lastIndexOf(".");
  if (dot > 0) {
    return extensionKinds[name.slice(dot + 1).toLowerCase()] ?? "file";
  }
  return "file";
}
