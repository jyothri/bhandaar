// Builds the Google Drive query (`q`) for a scan request from the form's
// options. See docs/archive/request-drive-scans.md, "Drive query builder".

export type DriveFileType = "images" | "videos" | "audio" | "pdfs" | "googleDocs";

const FOLDER = "application/vnd.google-apps.folder";

/** The file types the form offers, in order, with the term each adds. */
export const driveFileTypes: {
  value: DriveFileType;
  label: string;
  term: string;
}[] = [
  { value: "images", label: "Images", term: "mimeType contains 'image/'" },
  { value: "videos", label: "Videos", term: "mimeType contains 'video/'" },
  { value: "audio", label: "Audio", term: "mimeType contains 'audio/'" },
  { value: "pdfs", label: "PDFs", term: "mimeType = 'application/pdf'" },
  {
    value: "googleDocs",
    label: "Google Docs/Sheets/Slides",
    term: "mimeType contains 'application/vnd.google-apps.'",
  },
];

export type DriveQueryOptions = {
  ownedByMe: boolean;
  includeTrash: boolean;
  fileTypes: DriveFileType[];
  // YYYY-MM-DD, as <input type="date"> gives it, or "" when not set.
  startDate: string;
  endDate: string;
};

// Midnight UTC of a YYYY-MM-DD date shifted by `days`, as Drive compares
// times: RFC 3339 without a zone, which Drive reads as UTC.
function driveTime(input: string, days = 0): string {
  const [year, month, day] = input.split("-").map(Number);
  const date = new Date(Date.UTC(year, month - 1, day + days));
  return date.toISOString().slice(0, 10) + "T00:00:00";
}

export function buildDriveQuery({
  ownedByMe,
  includeTrash,
  fileTypes,
  startDate,
  endDate,
}: DriveQueryOptions): string {
  // Folders are skipped by the scan anyway.
  const terms = [`mimeType != '${FOLDER}'`];
  if (!includeTrash) {
    terms.push("trashed = false");
  }
  if (ownedByMe) {
    terms.push("'me' in owners");
  }
  const typeTerms = driveFileTypes
    .filter(({ value }) => fileTypes.includes(value))
    .map(({ term }) => term);
  if (typeTerms.length === 1) {
    terms.push(typeTerms[0]);
  } else if (typeTerms.length > 1) {
    terms.push(`(${typeTerms.join(" or ")})`);
  }
  if (startDate !== "") {
    terms.push(`modifiedTime >= '${driveTime(startDate)}'`);
  }
  if (endDate !== "") {
    // The next day, so that endDate is included.
    terms.push(`modifiedTime < '${driveTime(endDate, 1)}'`);
  }
  return terms.join(" and ");
}

const FOLDER_ID = /^[A-Za-z0-9_-]{10,}$/;

/**
 * The folder ID in a Drive folder link or a bare ID; "" for empty input,
 * and null when it's neither. The backend checks the same pattern.
 */
export function driveFolderId(input: string): string | null {
  const text = input.trim();
  if (text === "") {
    return "";
  }
  if (FOLDER_ID.test(text)) {
    return text;
  }
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return null;
  }
  if (url.hostname !== "drive.google.com") {
    return null;
  }
  // https://drive.google.com/drive/folders/<id>, also under /u/<n>/, or
  // https://drive.google.com/open?id=<id>.
  const id =
    url.pathname.match(/\/folders\/([^/]+)/)?.[1] ?? url.searchParams.get("id");
  return id !== null && FOLDER_ID.test(id) ? id : null;
}
