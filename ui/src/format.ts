// Display formatting shared by the progress and history views.

/** Formats seconds as m:ss, or h:mm:ss from an hour up. */
export function formatDuration(totalSeconds: number): string {
  const seconds = Math.max(0, Math.round(totalSeconds));
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = String(seconds % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
}

const dateTimeFormat = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

/** Formats an ISO timestamp in the viewer's locale and time zone. */
export function formatDateTime(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? iso : dateTimeFormat.format(date);
}

const byteUnits = ["B", "KB", "MB", "GB", "TB"];

/** Formats a byte count in binary units, e.g. 3.7 GB. */
export function formatBytes(bytes: number): string {
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < byteUnits.length - 1) {
    value /= 1024;
    unit++;
  }
  const digits = unit === 0 || value >= 100 ? 0 : 1;
  return `${value.toFixed(digits)} ${byteUnits[unit]}`;
}

const scanTypeLabels: Record<string, string> = {
  gmail: "Gmail",
  google_drive: "Google Drive",
  local: "Local",
  photos: "Google Photos",
};

/** A scan type as people read it, e.g. "Google Drive" for google_drive. */
export function scanTypeLabel(scanType: string): string {
  return scanTypeLabels[scanType] ?? scanType;
}
