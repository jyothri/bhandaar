import { Progress } from "./types/scans";

// Folds progress events from /sse/scanprogress into what the progress row
// shows.

export const isFinal = (progress: Progress) =>
  progress.status === "Completed" || progress.status === "Failed";

/**
 * Returns the row after `next` arrives. A scan's final event (Completed or
 * Failed) carries no counts, so it keeps the counts from earlier events.
 * Once a scan has ended, a late Running event for it is ignored; the final
 * event is published separately and can overtake the last Running one.
 */
export function mergeProgress(
  current: Progress | null,
  next: Progress
): Progress {
  if (current?.scan_id !== next.scan_id) {
    return next;
  }
  if (isFinal(current)) {
    return current;
  }
  if (isFinal(next)) {
    return { ...current, status: next.status, error: next.error };
  }
  return next;
}

const MAX_ERROR_LENGTH = 200;

/** A short, readable version of a failed scan's error. */
export function describeScanError(error: string | undefined): string {
  if (!error) {
    return "Unknown error";
  }
  if (error.includes("invalid_grant")) {
    return "Google rejected this account's saved sign-in (invalid_grant). Link the account again.";
  }
  const firstLine = error.split("\n")[0].trim();
  return firstLine.length > MAX_ERROR_LENGTH
    ? `${firstLine.slice(0, MAX_ERROR_LENGTH - 1)}…`
    : firstLine;
}
