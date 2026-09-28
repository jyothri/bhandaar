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

/**
 * How long ago an ISO timestamp was, roughly: "just now", "5m ago",
 * "2h ago", "3d ago", or a date past a month.
 */
export function formatAgo(iso: string, now: number = Date.now()): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) {
    return iso;
  }
  const minutes = Math.floor((now - then) / 60_000);
  if (minutes < 1) {
    return "just now";
  }
  if (minutes < 60) {
    return `${minutes}m ago`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours}h ago`;
  }
  const days = Math.floor(hours / 24);
  return days <= 30 ? `${days}d ago` : formatDateTime(iso);
}

const countFormat = new Intl.NumberFormat();

/** A count with thousands separators, e.g. 5,910. */
export function formatCount(n: number): string {
  return countFormat.format(n);
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

/**
 * A size's share of its folder's, as a percentage for a size bar. Anything
 * above nothing shows at least a sliver (min), so it still reads as there.
 */
export function shareOf(bytes: number, total: number, min = 0.5): number {
  if (bytes <= 0 || total <= 0) {
    return 0;
  }
  return Math.min(100, Math.max(min, (bytes / total) * 100));
}
